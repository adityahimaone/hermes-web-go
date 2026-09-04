package httpserver

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// state.db reconciliation (Python parity: models.py get_state_db_session_messages
// + reconciled_state_db_messages_for_session). The agent persists the full
// OpenAI-format transcript — including tool_call_id/tool_calls/tool_name rows,
// reasoning, and token metadata — into ~/.hermes/state.db keyed by the WebUI
// session id. The webui.db messages column only stores the WebUI-observed
// projection, which is missing tool metadata and can lag the agent during
// gateway-side turns. Python merges both on every GET /api/session read; the
// merged transcript is what renders "Processed" worklog groups (tool rows),
// context/token usage, and busy-state affordances.
//
// ponytail: python's full reconciliation handles compression anchors,
// continuation stitching (parent_session_id), context_messages preference, and
// per-profile state.db paths. This reader covers the dominant case — flat
// active-row merge by timestamp with sidecar tail fill — add stitching when a
// session actually has compressed ancestors.

type stateDBMessage struct {
	ID        int64
	Role      string
	Content   sql.NullString
	Timestamp float64
	Extra     map[string]any
}

// stateDBPath resolves the active profile's state.db (hermesHome/state.db).
func stateDBPath(hermesHome string) string {
	if hermesHome == "" {
		hermesHome = defaultHermesHome()
	}
	return filepath.Join(hermesHome, "state.db")
}

// readStateDBMessages loads active rows for sid from state.db, newest-last.
// Returns nil when state.db is absent or has no rows for the session.
func readStateDBMessages(hermesHome, sid string) []map[string]any {
	dbPath := stateDBPath(hermesHome)
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil
	}
	defer db.Close()
	// Read-only + SHORT busy timeout: state.db is owned by the agent, which
	// holds write locks frequently. A reader must never stall a turn's done
	// event behind agent write contention — a missed merge is recoverable on
	// the next GET /api/session, a stalled done is not.
	for _, pragma := range []string{"PRAGMA query_only=ON", "PRAGMA busy_timeout=250"} {
		_, _ = db.Exec(pragma)
	}
	rows, err := db.Query(`
		SELECT id, role, content, timestamp, tool_call_id, tool_calls, tool_name,
		       reasoning, token_count
		FROM messages
		WHERE session_id = ? AND (active IS NULL OR active != 0)
		ORDER BY id ASC`, sid)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var (
			id         int64
			role       string
			content    sql.NullString
			timestamp  float64
			toolCallID sql.NullString
			toolCalls  sql.NullString
			toolName   sql.NullString
			reasoning  sql.NullString
			tokenCount sql.NullInt64
		)
		if err := rows.Scan(&id, &role, &content, &timestamp, &toolCallID, &toolCalls, &toolName, &reasoning, &tokenCount); err != nil {
			continue
		}
		m := map[string]any{"role": role, "timestamp": timestamp}
		if content.Valid {
			m["content"] = content.String
		}
		if toolCallID.Valid && toolCallID.String != "" {
			m["tool_call_id"] = toolCallID.String
		}
		if toolName.Valid && toolName.String != "" {
			m["tool_name"] = toolName.String
		}
		if toolCalls.Valid && strings.TrimSpace(toolCalls.String) != "" {
			var tc any
			if json.Unmarshal([]byte(toolCalls.String), &tc) == nil {
				m["tool_calls"] = tc
			}
		}
		if reasoning.Valid && reasoning.String != "" {
			m["reasoning"] = reasoning.String
		}
		if tokenCount.Valid && tokenCount.Int64 > 0 {
			m["token_count"] = tokenCount.Int64
		}
		out = append(out, m)
	}
	return out
}

// reconcileSessionMessages merges the webui.db sidecar projection with
// state.db rows. state.db is the agent's authoritative transcript, but the
// sidecar can hold fresher rows (the just-finished turn the agent has not
// flushed yet — typically the final assistant message carrying _turnDuration).
// Sidecar rows that duplicate state.db tail rows (same role + content head)
// or predate the state.db tail are skipped; everything else is appended.
func reconcileSessionMessages(sidecar []map[string]any, stateRows []map[string]any) []map[string]any {
	if len(stateRows) == 0 {
		return sidecar
	}
	last := lastTimestamp(stateRows)
	const tailWindow = 40
	tailStart := len(stateRows) - tailWindow
	if tailStart < 0 {
		tailStart = 0
	}
	tailKeys := make(map[string]bool, tailWindow)
	for _, r := range stateRows[tailStart:] {
		tailKeys[messageContentKey(r)] = true
	}
	merged := stateRows
	for _, m := range sidecar {
		ts, _ := m["timestamp"].(float64)
		key := messageContentKey(m)
		if ts != 0 && ts <= last && !tailKeys[key] {
			continue // durable row already covered by state.db
		}
		if tailKeys[key] {
			continue // duplicate projection of a state.db tail row
		}
		merged = append(merged, m)
	}
	carryTurnMetaToLastAssistant(merged, sidecar)
	return merged
}

// carryTurnMetaToLastAssistant copies the WebUI's turn timing (_turnDuration,
// _turnTps, _firstTokenMs, _usedModel) onto the merged transcript's final
// assistant message when state.db's copy lacks it: the agent's state.db flush
// does not carry the WebUI-computed timing, and the settled UI renders
// "Processed in Xs" from that field.
func carryTurnMetaToLastAssistant(merged, sidecar []map[string]any) {
	var dst map[string]any
	for i := len(merged) - 1; i >= 0; i-- {
		if merged[i]["role"] == "assistant" {
			dst = merged[i]
			break
		}
	}
	if dst == nil || dst["_turnDuration"] != nil {
		return
	}
	for i := len(sidecar) - 1; i >= 0; i-- {
		m := sidecar[i]
		if m["role"] != "assistant" {
			continue
		}
		if m["_turnDuration"] == nil {
			continue
		}
		for _, k := range []string{"_turnDuration", "_turnTps", "_firstTokenMs", "_usedModel"} {
			if v, ok := m[k]; ok {
				dst[k] = v
			}
		}
		return
	}
}

// messageContentKey identifies a row by role + content head; both stores use
// the same content projection for user/assistant rows, so this is a reliable
// duplicate signal for fresh-tail reconciliation.
func messageContentKey(m map[string]any) string {
	role, _ := m["role"].(string)
	content, _ := m["content"].(string)
	if len(content) > 80 {
		content = content[:80]
	}
	return role + "\x00" + content
}

func lastTimestamp(rows []map[string]any) float64 {
	var last float64
	for _, r := range rows {
		if ts, ok := r["timestamp"].(float64); ok && ts > last {
			last = ts
		}
	}
	return last
}

// parseSidecarMessages decodes the webui.db messages JSON into generic maps;
// returns nil on empty/malformed (never fails the request).
func parseSidecarMessages(raw string) []map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var msgs []map[string]any
	if json.Unmarshal([]byte(raw), &msgs) != nil {
		return nil
	}
	return msgs
}
