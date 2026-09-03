package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

// conversationRoundThreshold mirrors api/models.py CONVERSATION_ROUND_THRESHOLD.
const conversationRoundThreshold = 10

// ConversationRoundsRouter serves POST /api/session/conversation-rounds,
// counting completed user→assistant rounds from the Hermes state.db (the same
// source Python's count_conversation_rounds reads). hermesHome resolves to
// the profile's Hermes home; state.db lives at <home>/state.db.
func ConversationRoundsRouter(r chi.Router, hermesHome string) {
	r.Post("/api/session/conversation-rounds", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			SessionID string `json:"session_id"`
			Since     any    `json:"since"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		sid := strings.TrimSpace(body.SessionID)
		if sid == "" {
			writeError(w, http.StatusBadRequest, "session_id is required")
			return
		}
		since, err := sinceFloat(body.Since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be a unix timestamp or ISO-8601 string")
			return
		}

		rounds, err := countConversationRounds(hermesHome, sid, since)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count conversation rounds")
			return
		}
		writeJSON(w, map[string]any{
			"ok":          true,
			"rounds":      rounds,
			"threshold":   conversationRoundThreshold,
			"should_show": rounds >= conversationRoundThreshold,
		})
	})
}

// sinceFloat normalizes the optional "since" body field. Python accepts a
// unix timestamp (int/float) or an ISO-8601 string; anything else is nil
// (no filter). A present-but-invalid value is an error.
func sinceFloat(v any) (float64, error) {
	if v == nil {
		return 0, nil
	}
	switch t := v.(type) {
	case float64:
		return t, nil
	case int64:
		return float64(t), nil
	case int:
		return float64(t), nil
	case string:
		if t == "" {
			return 0, nil
		}
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, nil
		}
		ts, err := time.Parse(time.RFC3339, strings.Replace(t, "Z", "+00:00", 1))
		if err != nil {
			return 0, err
		}
		return float64(ts.Unix()), nil
	default:
		return 0, nil
	}
}

// countConversationRounds mirrors api/models.py count_conversation_rounds:
// one round = a user message followed by an agent reply, consecutive user
// messages merged. Reads state.db read-only; no state.db → 0 (not an error).
func countConversationRounds(home, sid string, since float64) (int, error) {
	if home == "" {
		return 0, nil
	}
	dbPath := filepath.Join(home, "state.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil || db == nil {
		return 0, nil
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return 0, nil // state.db absent/unreadable — Python returns 0
	}

	var role string
	var tsRaw any
	rows, err := db.Query(`SELECT role, timestamp FROM messages WHERE session_id = ? ORDER BY timestamp ASC`, sid)
	if err != nil {
		return 0, nil
	}
	defer rows.Close()

	rounds := 0
	seenUser := false
	seenAgentAfterUser := false
	for rows.Next() {
		if err := rows.Scan(&role, &tsRaw); err != nil {
			return 0, nil
		}
		if since > 0 && tsRaw != nil {
			tsVal, ok := tsFloat(tsRaw)
			if ok && tsVal <= since {
				continue
			}
		}
		role = strings.TrimSpace(strings.ToLower(role))
		switch role {
		case "user":
			if seenUser && !seenAgentAfterUser {
				// consecutive user message — merge
			} else if seenUser && seenAgentAfterUser {
				rounds++
				seenAgentAfterUser = false
			}
			seenUser = true
		case "assistant":
			if seenUser {
				seenAgentAfterUser = true
			}
		}
	}
	if seenUser && seenAgentAfterUser {
		rounds++
	}
	return rounds, nil
}

// tsFloat converts a state.db timestamp row (int/float Unix or ISO string)
// to a Unix float. Returns ok=false when unparseable (Python skips filter).
func tsFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int64:
		return float64(t), true
	case float64:
		return t, true
	case []uint8:
		s := string(t)
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
		ts, err := time.Parse(time.RFC3339, strings.Replace(s, "Z", "+00:00", 1))
		if err != nil {
			return 0, false
		}
		return float64(ts.Unix()), true
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, true
		}
		ts, err := time.Parse(time.RFC3339, strings.Replace(t, "Z", "+00:00", 1))
		if err != nil {
			return 0, false
		}
		return float64(ts.Unix()), true
	default:
		return 0, false
	}
}
