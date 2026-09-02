package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/approval"
)

// ApprovalRouter mounts approval polling/respond endpoints. Approval state is
// process-local, matching Python's module-level approval state for Phase 5.
func ApprovalRouter(r chi.Router, st *approval.Store) {
	if st == nil {
		return
	}
	r.Get("/api/approval/pending", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		entry, ok := st.Pending(sid)
		if !ok {
			writeJSON(w, map[string]any{"pending": nil, "pending_count": 0})
			return
		}
		writeJSON(w, map[string]any{"pending": approvalPayload(entry), "pending_count": st.Count(sid)})
	})

	r.Post("/api/approval/respond", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID  string `json:"session_id"`
			ApprovalID string `json:"approval_id"`
			Choice     string `json:"choice"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.SessionID == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		if body.Choice == "" {
			body.Choice = "deny"
		}
		if body.Choice != "once" && body.Choice != "session" && body.Choice != "always" && body.Choice != "deny" {
			writeError(w, http.StatusBadRequest, "Invalid choice: "+body.Choice)
			return
		}
		target := body.ApprovalID
		if target == "" {
			entry, ok := st.Pending(body.SessionID)
			if !ok {
				writeError(w, http.StatusConflict, "approval not pending")
				return
			}
			target = entry.ID
		}
		if !st.Respond(body.SessionID, target, body.Choice) {
			writeError(w, http.StatusConflict, "approval not pending")
			return
		}
		writeJSON(w, map[string]any{"ok": true, "choice": body.Choice})
	})
}

func approvalPayload(entry approval.PendingApproval) map[string]any {
	return map[string]any{
		"approval_id":  entry.ID,
		"session_id":   entry.SessionID,
		"command":      entry.Command,
		"description":  entry.Description,
		"pattern_keys": entry.PatternKeys,
		"run_id":       entry.RunID,
	}
}
