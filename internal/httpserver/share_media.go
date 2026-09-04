package httpserver

// Wave 20 — public share reads + revoke (Python ports, routes.py 13079-13091
// + 15246-15279, shares.py 45-57 / 451-540).
//
//   GET  /api/share/{token} — sanitized public snapshot (revoked → 404)
//   POST /api/share/revoke  — tombstone the snapshot, clear session fields
//
// Deliberately NOT ported here (stays proxied to Python):
//   POST /api/share/create — the snapshot builder embeds the agent's
//     force-redaction engine (agent/redact.py, ~1500 lines) as an ALWAYS-ON
//     public-boundary guard plus MEDIA: base64 embedding with magic-byte
//     checks. Half-porting a public security boundary is worse than not
//     porting it; Python remains authoritative for share creation.
//   GET  /api/media — snapshot digests (?snap=sha256) + session media tokens
//     + CSP-sandboxed HTML; coupled to api/media_snapshots.py state.
//
// Parity notes: token charset alnum/-/_ (paths confined to SHARES_DIR),
// revoked snapshots are invisible to reads but keep their file (tombstone),
// reads emit Cache-Control: no-store + X-Robots-Tag: noindex, nofollow.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
)

// validShareToken mirrors shares.py _share_path(): only [A-Za-z0-9_-] allowed.
func validShareToken(token string) bool {
	if token == "" {
		return false
	}
	for _, c := range token {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

func shareFilePath(dataRoot, token string) string {
	return filepath.Join(dataRoot, "shares", token+".json")
}

// handleShareGet serves GET /api/share/{token}.
func handleShareGet(w http.ResponseWriter, r *http.Request, dataRoot string) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if !validShareToken(token) {
		writeError(w, http.StatusNotFound, "Shared conversation not found")
		return
	}
	raw, err := os.ReadFile(shareFilePath(dataRoot, token))
	if err != nil {
		writeError(w, http.StatusNotFound, "Shared conversation not found")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		writeError(w, http.StatusNotFound, "Shared conversation not found")
		return
	}
	// Python: any truthy revoked_at hides the share (null/absent = visible).
	if v, ok := payload["revoked_at"]; ok && v != nil {
		if f, isF := v.(float64); !isF || f != 0 {
			writeError(w, http.StatusNotFound, "Shared conversation not found")
			return
		}
	}

	messages, _ := payload["messages"].([]any)
	if messages == nil {
		messages = []any{}
	}
	title, _ := payload["title"].(string)
	if title == "" {
		title = "Untitled"
	}
	messageCount := len(messages)
	if mc, ok := payload["message_count"].(float64); ok && mc > 0 {
		messageCount = int(mc)
	}
	public := map[string]any{
		"title":         title,
		"messages":      messages,
		"message_count": messageCount,
	}
	if ca, ok := payload["created_at"].(float64); ok {
		public["created_at"] = ca
	}
	if ua, ok := payload["updated_at"].(float64); ok {
		public["updated_at"] = ua
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	writeJSON(w, map[string]any{"share": public})
}

// handleShareRevoke serves POST /api/share/revoke {session_id}.
func handleShareRevoke(db *sql.DB, dataRoot string, body map[string]json.RawMessage) (int, map[string]any) {
	sid := strings.TrimSpace(jsonStrField(body["session_id"]))
	if sid == "" {
		return http.StatusBadRequest, map[string]any{"error": "session_id is required"}
	}
	if _, err := store.GetSession(db, sid); err != nil {
		return http.StatusNotFound, map[string]any{"error": "Session not found"}
	}

	// Token source 1: the WebUI session sidecar JSON.
	sessJSONPath := filepath.Join(dataRoot, "sessions", sid+".json")
	token := ""
	if raw, err := os.ReadFile(sessJSONPath); err == nil {
		var sj map[string]any
		if json.Unmarshal(raw, &sj) == nil && sj != nil {
			if t, ok := sj["share_token"].(string); ok {
				token = strings.TrimSpace(t)
			}
		}
	}
	// Token source 2 (parity with the CLI-session sidecar path): scan the
	// shares dir for a live snapshot bound to this source session.
	if token == "" {
		if entries, err := os.ReadDir(filepath.Join(dataRoot, "shares")); err == nil {
			for _, e := range entries {
				name := e.Name()
				if !strings.HasSuffix(name, ".json") || !validShareToken(strings.TrimSuffix(name, ".json")) {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(dataRoot, "shares", name))
				if err != nil {
					continue
				}
				var sj map[string]any
				if json.Unmarshal(raw, &sj) != nil || sj == nil {
					continue
				}
				if src, _ := sj["source_session_id"].(string); src == sid {
					if rev, hasRev := sj["revoked_at"]; hasRev && rev != nil {
						if f, isF := rev.(float64); !isF || f != 0 {
							continue // already tombstoned
						}
					}
					token = strings.TrimSuffix(name, ".json")
					break
				}
			}
		}
	}
	if token == "" || !validShareToken(token) {
		return http.StatusNotFound, map[string]any{"error": "Session not found"}
	}

	// Tombstone the snapshot (keep the file, set revoked_at — shares.py parity).
	sPath := shareFilePath(dataRoot, token)
	payload := map[string]any{"revoked_at": float64(time.Now().UnixMilli()) / 1000.0}
	if raw, err := os.ReadFile(sPath); err == nil {
		var existing map[string]any
		if json.Unmarshal(raw, &existing) == nil && existing != nil {
			existing["revoked_at"] = payload["revoked_at"]
			payload = existing
		}
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	if err := os.WriteFile(sPath, encoded, 0o644); err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}

	// Clear the session sidecar fields when a session JSON exists.
	if raw, err := os.ReadFile(sessJSONPath); err == nil {
		var sj map[string]any
		if json.Unmarshal(raw, &sj) == nil && sj != nil {
			sj["share_token"] = nil
			sj["share_created_at"] = nil
			if out, mErr := json.Marshal(sj); mErr == nil {
				tmp := sessJSONPath + ".tmp"
				if wErr := os.WriteFile(tmp, out, 0o644); wErr == nil {
					_ = os.Rename(tmp, sessJSONPath) // atomic replace parity
				}
			}
		}
	}

	// Response parity: {ok, session: <public projection with messages>}.
	row, err := store.GetSession(db, sid)
	if err != nil {
		return http.StatusOK, map[string]any{"ok": true}
	}
	sess := sessionResponse(row)
	sess["share_token"] = nil
	sess["share_created_at"] = nil
	return http.StatusOK, map[string]any{"ok": true, "session": sess}
}

// Wave20Router mounts the wave-20 share endpoints.
func Wave20Router(r chi.Router, db *sql.DB, dataRoot string) {
	r.Get("/api/share/{token}", func(w http.ResponseWriter, req *http.Request) {
		handleShareGet(w, req, dataRoot)
	})
	r.Post("/api/share/revoke", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]json.RawMessage
		if !decodeJSONBody(w, req, &body) {
			return
		}
		st, payload := handleShareRevoke(db, dataRoot, body)
		writeJSONStatus(w, st, payload)
	})
}
