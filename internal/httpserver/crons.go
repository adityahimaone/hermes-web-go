package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/crons"
)

// CronsRouter mounts the native cron panel routes onto r. The two file-backed
// reads are always native. Mutations forward scheduler ownership to the agent
// gateway when a CronMutator is available; otherwise they 503 (proxy-free).
func CronsRouter(r chi.Router, home string, mut agentclient.CronMutator) {
	r.Get("/api/crons", func(w http.ResponseWriter, r *http.Request) {
		jobs, err := crons.List(home)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read cron jobs")
			return
		}
		writeJSON(w, map[string]any{
			"jobs":                jobs,
			"all_profiles":        false,
			"active_profile":      "default",
			"other_profile_count": 0,
		})
	})

	r.Get("/api/crons/output", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Query().Get("job_id")
		limit := 5
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				limit = n
			}
		}
		outputs, err := crons.Output(home, jobID, limit)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"job_id": jobID, "outputs": outputs})
	})

	// GET /api/crons/recent — poll for recently finished runs (toast source).
	// `since` epoch seconds; returns only jobs whose last run is newer.
	r.Get("/api/crons/recent", func(w http.ResponseWriter, r *http.Request) {
		since := 0.0
		if raw := r.URL.Query().Get("since"); raw != "" {
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				since = f
			}
		}
		recent, err := crons.Recent(home, since)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read cron jobs")
			return
		}
		writeJSON(w, map[string]any{"completions": recent, "since": since})
	})

	// GET /api/crons/history — output file metadata list for a job.
	r.Get("/api/crons/history", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Query().Get("job_id")
		offset, limit := 0, 50
		if raw := r.URL.Query().Get("offset"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				offset = n
			}
		}
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				limit = n
			}
		}
		items, total, err := crons.History(home, jobID, offset, limit)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"job_id": jobID, "runs": items, "total": total, "offset": offset})
	})

	// GET /api/crons/run — fetch a specific output file (frontend calls this from output panel)
	r.Get("/api/crons/run", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Query().Get("job_id")
		filename := r.URL.Query().Get("filename")
		if jobID == "" || !crons.ValidJobID(jobID) {
			writeError(w, http.StatusBadRequest, "invalid job_id")
			return
		}
		// fetch up to 50 latest outputs
		outputs, err := crons.Output(home, jobID, 50)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var content string
		if filename != "" {
			// find exact filename match
			for _, out := range outputs {
				if f, ok := out["filename"].(string); ok && f == filename {
					content, _ = out["content"].(string)
					break
				}
			}
		} else if len(outputs) > 0 {
			// no filename → newest
			content, _ = outputs[0]["content"].(string)
		}
		if content == "" {
			writeJSON(w, map[string]any{"error": "output not found"})
			return
		}
		// frontend expects {content, snippet, usage}
		snippet := content
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		writeJSON(w, map[string]any{
			"content": content,
			"snippet": snippet,
			"usage":   nil, // not available from file
		})
	})

	// GET /api/crons/status — running status for one or all cron jobs.
	// Process-local run tracking lives in the agent, so all-jobs view reports
	// an empty map and single-job reports running=false (ponytail: expose
	// agent-side _RUNNING_CRON_JOBS via gateway once the cron panel needs it).
	r.Get("/api/crons/status", func(w http.ResponseWriter, r *http.Request) {
		jobID := r.URL.Query().Get("job_id")
		if jobID != "" {
			writeJSON(w, map[string]any{"job_id": jobID, "running": false, "elapsed": 0.0})
			return
		}
		writeJSON(w, map[string]any{"running": map[string]any{}})
	})

	// mutations — 503 unless a gateway mutator is wired (proxy-free cutover)
	r.Post("/api/crons/create", func(w http.ResponseWriter, r *http.Request) {
		mutateCron(w, r, mut, "create")
	})
	r.Post("/api/crons/update", func(w http.ResponseWriter, r *http.Request) {
		mutateCron(w, r, mut, "update")
	})
	r.Post("/api/crons/delete", func(w http.ResponseWriter, r *http.Request) {
		mutateCron(w, r, mut, "delete")
	})
	r.Post("/api/crons/pause", func(w http.ResponseWriter, r *http.Request) {
		mutateCron(w, r, mut, "pause")
	})
	r.Post("/api/crons/resume", func(w http.ResponseWriter, r *http.Request) {
		mutateCron(w, r, mut, "resume")
	})
	r.Post("/api/crons/run", func(w http.ResponseWriter, r *http.Request) {
		mutateCron(w, r, mut, "run")
	})

	r.Get("/api/crons/delivery-options", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"platforms": deliveryPlatforms()})
	})
}

// mutateCron reads the legacy POST body, validates job_id for id-scoped
// actions, forwards to the gateway, and maps the gateway REST response back to
// the legacy WebUI shape {ok, job, job_id}.
func mutateCron(w http.ResponseWriter, r *http.Request, mut agentclient.CronMutator, action string) {
	if mut == nil {
		writeError(w, http.StatusServiceUnavailable, "cron mutations unavailable (no agent mutator)")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	jobID, _ := body["job_id"].(string)
	if action != "create" {
		if jobID == "" || !crons.ValidJobID(jobID) {
			writeError(w, http.StatusBadRequest, "invalid job_id")
			return
		}
	}
	profile, _ := body["profile"].(string)
	// strip profile from forwarded body; gateway selects it via URL prefix
	delete(body, "profile")
	payload, _ := json.Marshal(body)

	status, respBody, err := mut.CronMutation(r.Context(), action, jobID, profile, payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cron mutation failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if len(respBody) == 0 {
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	// Map gateway REST response into the legacy {ok, job, job_id} envelope.
	writeMappedCron(w, status, respBody, action, jobID)
}

func writeMappedCron(w http.ResponseWriter, status int, respBody []byte, action, jobID string) {
	var gresp struct {
		Job map[string]any `json:"job"`
		Ok  bool           `json:"ok"`
	}
	_ = json.Unmarshal(respBody, &gresp)
	legacy := map[string]any{"ok": true}
	if gresp.Job != nil {
		legacy["job"] = gresp.Job
	}
	if action == "delete" {
		legacy["job_id"] = jobID
	}
	writeJSON(w, legacy)
}

func deliveryPlatforms() []map[string]string {
	// Mirrors cron.scheduler _KNOWN_DELIVERY_PLATFORMS defaults.
	return []map[string]string{
		{"value": "local", "label": "Local (save output only)"},
		{"value": "origin", "label": "Origin (reply to creator)"},
	}
}
