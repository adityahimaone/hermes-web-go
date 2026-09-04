package httpserver

// Wave 12 — handoff-summary (fallback) + recovery/repair-safe.
//
// POST /api/session/handoff-summary — no LLM in the Go proxy: validate the
//   session, count rounds + read the transcript from state.db (same source as
//   Python), and return the deterministic fallback summary with fallback=true.
//   Python uses the same fallback when its LLM call fails; the summary is
//   display-only (empty tool_call_id keeps it out of model context).
// POST /api/session/recovery/repair-safe — safe deterministic repairs over the
//   WebUI sessions dir: restore shrunken live files from .bak, materialize
//   missing state.db sidecars, rebuild the recovery index. Returns
//   before/after audits; 200 when clean, 409 otherwise (parity).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// ── handoff summary ────────────────────────────────────────────────────────

type handoffMsg struct {
	Role      string
	Content   string
	Timestamp float64
}

// stateDBTranscript reads role/content/timestamp rows from state.db for a
// session (stitch_continuations is a Python-side nicety; the fallback summary
// only needs recent user/assistant text — deviation documented).
func stateDBTranscript(home, sid string) []handoffMsg {
	db, cols, err := stateDBSessions(home)
	if err != nil || !cols["id"] {
		return nil
	}
	defer db.Close()
	msgCols := map[string]bool{}
	rows, err := db.Query("PRAGMA table_info(messages)")
	if err != nil {
		return nil
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err == nil {
			msgCols[name] = true
		}
	}
	rows.Close()
	if !msgCols["session_id"] || !msgCols["role"] || !msgCols["content"] {
		return nil
	}
	order := "rowid"
	if msgCols["timestamp"] && msgCols["id"] {
		order = "timestamp, id"
	}
	tsExpr := "NULL AS timestamp"
	if msgCols["timestamp"] {
		tsExpr = "timestamp"
	}
	q := fmt.Sprintf("SELECT role, content, %s FROM messages WHERE session_id = ? ORDER BY %s", tsExpr, order)
	rows2, err := db.Query(q, sid)
	if err != nil {
		return nil
	}
	defer rows2.Close()
	var out []handoffMsg
	for rows2.Next() {
		var role, content string
		var ts any
		if rows2.Scan(&role, &content, &ts) != nil {
			continue
		}
		out = append(out, handoffMsg{Role: strings.TrimSpace(strings.ToLower(role)), Content: content, Timestamp: asFloat(ts)})
	}
	return out
}

func extractHandoffText(content any) string {
	switch c := content.(type) {
	case string:
		return strings.TrimSpace(c)
	case []any:
		parts := []string{}
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				if t, ok := m["text"].(string); ok && t != "" {
					parts = append(parts, t)
				} else if t, ok := m["content"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	}
	return strings.TrimSpace(fmt.Sprintf("%v", content))
}

func summarizeSnippet(raw string, maxLen int) string {
	text := strings.Join(strings.Fields(raw), " ")
	if len(text) <= maxLen {
		return text
	}
	return strings.TrimRight(text[:maxLen-1], "") + "…"
}

func fallbackHandoffSummary(msgs []handoffMsg, chinese bool) string {
	var userPoints, asstPoints []string
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		snippet := summarizeSnippet(m.Content, 82)
		if snippet == "" {
			continue
		}
		if m.Role == "user" {
			userPoints = append(userPoints, snippet)
		} else {
			asstPoints = append(asstPoints, snippet)
		}
	}
	if len(userPoints) == 0 && len(asstPoints) == 0 {
		if chinese {
			return "近期可读文本不足，无法生成更完整的交接摘要，请补充一条消息后重试。"
		}
		return "Not enough readable text to create a useful handoff summary; please send one more message and retry."
	}
	bullets := []string{}
	if chinese {
		if len(userPoints) > 0 {
			bullets = append(bullets, fmt.Sprintf("- 你刚讨论了：%s。", userPoints[len(userPoints)-1]))
		}
		if len(asstPoints) > 0 {
			bullets = append(bullets, fmt.Sprintf("- 助手已回复：%s。", asstPoints[len(asstPoints)-1]))
		}
		if len(userPoints)+len(asstPoints) >= 2 {
			bullets = append(bullets, "- 当前对话存在尚未确认的后续动作。")
		} else {
			bullets = append(bullets, "- 当前信息偏少，建议补充关键点后再切换。")
		}
		return strings.Join(bullets, "\n")
	}
	if len(userPoints) > 0 {
		bullets = append(bullets, fmt.Sprintf("- You asked: %s.", userPoints[len(userPoints)-1]))
	}
	if len(asstPoints) > 0 {
		bullets = append(bullets, fmt.Sprintf("- The assistant responded: %s.", asstPoints[len(asstPoints)-1]))
	}
	if len(userPoints)+len(asstPoints) >= 2 {
		bullets = append(bullets, "- There is pending context to continue next.")
	} else {
		bullets = append(bullets, "- The conversation is still short; add one more turn before summarizing.")
	}
	return strings.Join(bullets, "\n")
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

func handleHandoffSummary(db *sql.DB, hermesHome string, body map[string]any) (int, map[string]any) {
	sid, _ := body["session_id"].(string)
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	var one int
	if err := db.QueryRow("SELECT 1 FROM sessions WHERE session_id = ?", sid).Scan(&one); err != nil {
		return 404, map[string]any{"error": "Session not found"}
	}
	since := 0.0
	if v, ok := body["since"]; ok && v != nil {
		since = asFloat(v)
		if since == 0 {
			return 400, map[string]any{"error": "since must be a unix timestamp (number)"}
		}
	}
	// threshold check from state.db (same source as Python)
	all := stateDBTranscript(hermesHome, sid)
	rounds := countRoundsFromMsgs(all, since)
	if rounds < conversationRoundThreshold {
		return 400, map[string]any{"error": "Not enough conversation rounds to generate a summary."}
	}
	var filtered []handoffMsg
	for _, m := range all {
		if since > 0 && m.Timestamp > 0 && m.Timestamp <= since {
			continue
		}
		filtered = append(filtered, m)
	}
	if len(filtered) > 50 {
		filtered = filtered[len(filtered)-50:]
	}
	if len(filtered) < 2 {
		return 400, map[string]any{"error": "Not enough messages to summarize."}
	}
	// transcript lines: user/assistant text capped at 1000 chars
	chinese := false
	for _, m := range filtered {
		if containsHan(m.Content) {
			chinese = true
			break
		}
	}
	summary := fallbackHandoffSummary(filtered, chinese)
	return 200, map[string]any{
		"ok":            true,
		"summary":       summary,
		"message_count": len(filtered),
		"rounds":        rounds,
		"fallback":      true,
		"warning":       "Summary generation used local fallback: Go proxy has no LLM runtime",
	}
}

func countRoundsFromMsgs(msgs []handoffMsg, since float64) int {
	rounds := 0
	seenUser := false
	seenAgentAfterUser := false
	for _, m := range msgs {
		if since > 0 && m.Timestamp > 0 && m.Timestamp <= since {
			continue
		}
		switch m.Role {
		case "user":
			if seenUser && seenAgentAfterUser {
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
	return rounds
}

// ── recovery repair-safe ───────────────────────────────────────────────────

// repairSafeSessionRecovery: restore shrunken live from .bak, materialize
// missing sidecars from state.db, rebuild index. before/after audits.
func repairSafeSessionRecovery(home, sessionDir string) (int, map[string]any) {
	before := auditSessionRecovery(home, sessionDir)
	repaired := 0

	entries, _ := os.ReadDir(sessionDir)
	// 1. restore shrunken live files from .bak (bak_count > live_count)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "_") {
			continue
		}
		live := filepath.Join(sessionDir, name)
		bak := live + ".bak"
		if _, err := os.Stat(bak); err != nil {
			continue
		}
		if msgCount(bak) > msgCount(live) {
			if raw, err := os.ReadFile(bak); err == nil {
				if err := os.WriteFile(live, raw, 0o644); err == nil {
					repaired++
				}
			}
		}
	}
	// 2. materialize missing sidecars from state.db webui rows (non-empty)
	missing, _, emptyMsgs := stateDBWebuiRowsWithSidecarState(home, sessionDir)
	for _, row := range missing {
		sid := asStr(row["id"])
		if sid == "" || emptyMsgs[sid] || row["_state_db_deleted_webui_tombstone"] == true {
			continue
		}
		if materializeSidecar(home, sessionDir, sid) {
			repaired++
		}
	}
	// 3. rebuild index
	rebuildRecoveryIndex(sessionDir)

	after := auditSessionRecovery(home, sessionDir)
	afterSummary := after["summary"].(map[string]any)
	clean := afterSummary["unsafe_to_repair"].(int) == 0 && afterSummary["repairable"].(int) == 0
	code := 200
	if !clean {
		code = 409
	}
	return code, map[string]any{
		"clean":    clean,
		"ok":       clean,
		"repaired": repaired,
		"before":   before,
		"after":    after,
	}
}

func materializeSidecar(home, sessionDir, sid string) bool {
	db, cols, err := stateDBSessions(home)
	if err != nil || !cols["id"] {
		return false
	}
	defer db.Close()
	expr := func(name string) string {
		if cols[name] {
			return "COALESCE(" + name + ", '')"
		}
		return "''"
	}
	exprN := func(name string) string {
		if cols[name] {
			return "COALESCE(" + name + ", 0)"
		}
		return "0"
	}
	var title, model, workspace string
	var started float64
	q := fmt.Sprintf("SELECT %s, %s, %s, %s FROM sessions WHERE id = ?",
		expr("title"), expr("model"), expr("workspace"), exprN("started_at"))
	if err := db.QueryRow(q, sid).Scan(&title, &model, &workspace, &started); err != nil {
		return false
	}
	// messages from state.db
	type mRow struct {
		Role    string
		Content string
		TS      any
	}
	var mrows []mRow
	if mc, err2 := db.Query(`SELECT role, content, timestamp FROM messages WHERE session_id = ? ORDER BY rowid`, sid); err2 == nil {
		defer mc.Close()
		for mc.Next() {
			var r, c string
			var ts any
			if mc.Scan(&r, &c, &ts) == nil {
				mrows = append(mrows, mRow{Role: r, Content: c, TS: ts})
			}
		}
	}
	payload := map[string]any{
		"session_id":   sid,
		"title":        title,
		"model":        model,
		"workspace":    workspace,
		"source":       "webui",
		"started_at":   started,
		"messages":     []any{},
		"recovered":    true,
		"recovered_at": time.Now().Unix(),
	}
	msgs := []any{}
	for _, m := range mrows {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if m.TS != nil {
			msg["timestamp"] = m.TS
		}
		msgs = append(msgs, msg)
	}
	payload["messages"] = msgs
	raw, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return os.WriteFile(filepath.Join(sessionDir, sid+".json"), raw, 0o644) == nil
}

func rebuildRecoveryIndex(sessionDir string) {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return
	}
	idx := map[string]any{
		"version":    1,
		"rebuilt_at": time.Now().Unix(),
		"sessions":   map[string]any{},
	}
	sessions := idx["sessions"].(map[string]any)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), "_") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		p := filepath.Join(sessionDir, name)
		sid := strings.TrimSuffix(name, ".json")
		sessions[sid] = map[string]any{
			"messages": msgCount(p),
			"size":     fileSizeOr(p, 0),
			"mtime_ns": fileMtimeOr(p, 0),
		}
	}
	raw, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(sessionDir, "_index.json"), raw, 0o644)
}

func fileSizeOr(p string, def int64) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return def
	}
	return st.Size()
}

func fileMtimeOr(p string, def int64) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return def
	}
	return st.ModTime().UnixNano()
}

// ── router ─────────────────────────────────────────────────────────────────

// Wave12Router serves handoff-summary + repair-safe.
func Wave12Router(r chi.Router, db *sql.DB, hermesHome string) {
	r.Post("/api/session/handoff-summary", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleHandoffSummary(db, hermesHome, body)
		wave4WriteJSON(w, code, payload)
	})
	r.Post("/api/session/recovery/repair-safe", func(w http.ResponseWriter, req *http.Request) {
		dir := webuiSessionsDir(hermesHome)
		code, payload := repairSafeSessionRecovery(hermesHome, dir)
		wave4WriteJSON(w, code, payload)
	})
}
