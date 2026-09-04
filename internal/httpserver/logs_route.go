package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/logs"
)

// LogsRouter mounts the native /api/logs tail route.
func LogsRouter(r chi.Router, home string) {
	r.Get("/api/logs", func(w http.ResponseWriter, req *http.Request) {
		fileKey := req.URL.Query().Get("file")
		if fileKey == "" {
			fileKey = "agent"
		}
		tail, err := logs.Read(home, fileKey, req.URL.Query().Get("tail"))
		if err == logs.ErrUnknownLogFile {
			writeError(w, http.StatusBadRequest, "Unknown log file")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read log file")
			return
		}
		writeJSON(w, map[string]any{
			"file":        tail.File,
			"tail":        tail.Tail,
			"lines":       tail.Lines,
			"truncated":   tail.Truncated,
			"total_bytes": tail.TotalBytes,
			"mtime":       tail.Mtime,
			"hint":        tail.Hint,
		})
	})
}