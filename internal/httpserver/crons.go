package httpserver

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/crons"
)

// CronsRouter mounts the native read-only cron panel routes onto r.
// Mutations (create/pause/resume/run) stay behind the proxy — the agent-side
// scheduler owns state — so only the two file-backed reads are native.
func CronsRouter(r chi.Router, home string) {
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
}
