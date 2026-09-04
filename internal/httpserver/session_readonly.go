package httpserver

// Wave 11 — session read-only family: lineage/report + recovery/audit.
//
// GET /api/session/lineage/report — bounded read-only lifecycle report from
//   the profile's state.db (sessions table): walk parent_session_id chain by
//   the sidebar continuation rules, list child branches, flag manual_review.
//   Mirrors api/agent_sessions.read_session_lineage_report.
// GET /api/session/recovery/audit — read-only audit of the WebUI sessions dir
//   (HERMES_WEBUI_STATE_DIR or <hermes_home>/webui/sessions): shrunken live
//   files, orphan .bak files, index gaps, state.db missing sidecars. Never
//   mutates. Mirrors api/session_recovery.audit_session_recovery (core paths).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ── shared: state.db sessions table access ─────────────────────────────────

type lineageRow struct {
	ID              any
	Source          any
	SessionSource   any
	Title           any
	StartedAt       any
	ParentSessionID any
	EndedAt         any
	EndReason       any
}

func asStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case []byte:
		f, _ := strconv.ParseFloat(string(n), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

// continuationSession mirrors _is_continuation_session.
func continuationSession(parent, child lineageRow) bool {
	if asStr(child.SessionSource) == "fork" {
		return false
	}
	ps := strings.ToLower(strings.TrimSpace(asStr(parent.Source)))
	cs := strings.ToLower(strings.TrimSpace(asStr(child.Source)))
	if ps != "" && cs != "" && ps != cs {
		return false
	}
	if asStr(parent.EndReason) != "compression" && asStr(parent.EndReason) != "cli_close" {
		return false
	}
	if parent.EndedAt == nil {
		return true
	}
	return asFloat(child.StartedAt) >= asFloat(parent.EndedAt)
}

// stateDBSessions opens <home>/state.db read-only and reports which optional
// columns the sessions table has (old schemas degrade gracefully).
func stateDBSessions(home string) (*sql.DB, map[string]bool, error) {
	if home == "" {
		return nil, nil, fmt.Errorf("no hermes home")
	}
	dbPath := filepath.Join(home, "state.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, nil, err
	}
	cols := map[string]bool{}
	rows, err := db.Query("PRAGMA table_info(sessions)")
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var ctype string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err == nil {
			cols[name] = true
		}
	}
	return db, cols, nil
}

// fetchLineageRow reads one sessions row with only existing columns.
func fetchLineageRow(db *sql.DB, cols map[string]bool, id string) (lineageRow, bool) {
	expr := func(name, fallback string) string {
		if cols[name] {
			return name
		}
		return fallback + " AS " + name
	}
	q := "SELECT " + strings.Join([]string{
		"id",
		expr("source", "NULL"),
		expr("session_source", "NULL"),
		expr("title", "NULL"),
		expr("started_at", "'0'"),
		expr("parent_session_id", "NULL"),
		expr("ended_at", "NULL"),
		expr("end_reason", "NULL"),
	}, ", ") + " FROM sessions WHERE id = ?"
	var r lineageRow
	err := db.QueryRow(q, id).Scan(&r.ID, &r.Source, &r.SessionSource, &r.Title,
		&r.StartedAt, &r.ParentSessionID, &r.EndedAt, &r.EndReason)
	return r, err == nil
}

func lineageReportRow(r lineageRow, role string) map[string]any {
	updated := r.EndedAt
	if updated == nil {
		updated = r.StartedAt
	}
	return map[string]any{
		"session_id": r.ID,
		"role":       role,
		"title":      r.Title,
		"source":     r.Source,
		"started_at": r.StartedAt,
		"updated_at": updated,
		"end_reason": r.EndReason,
		"active":     r.EndedAt == nil,
		"archived":   false,
	}
}

func emptyLineageReport(sid string, found bool) map[string]any {
	return map[string]any{
		"mutation":              false,
		"found":                 found,
		"session_id":            sid,
		"lineage_key":           sid,
		"tip_session_id":        sid,
		"total_segments":        0,
		"materialized_segments": 0,
		"segments":              []any{},
		"children":              []any{},
		"manual_review":         false,
	}
}

// readSessionLineageReport mirrors read_session_lineage_report (max 20 hops).
func readSessionLineageReport(home, sid string) map[string]any {
	if strings.TrimSpace(sid) == "" {
		return emptyLineageReport("", false)
	}
	db, cols, err := stateDBSessions(home)
	if err != nil {
		return emptyLineageReport(sid, false)
	}
	defer db.Close()
	for _, req := range []string{"id", "parent_session_id", "end_reason"} {
		if !cols[req] {
			return emptyLineageReport(sid, false)
		}
	}
	target, ok := fetchLineageRow(db, cols, sid)
	if !ok {
		return emptyLineageReport(sid, false)
	}
	const maxHops = 20
	segments := []lineageRow{target}
	seen := map[string]bool{sid: true}
	current := target
	manualReview := false
	chain := true
	for hop := 0; hop < maxHops && chain; hop++ {
		pid := strings.TrimSpace(asStr(current.ParentSessionID))
		if pid == "" {
			break
		}
		parent, ok := fetchLineageRow(db, cols, pid)
		if !ok || seen[pid] {
			manualReview = pid != "" && seen[pid]
			break
		}
		if !continuationSession(parent, current) {
			break
		}
		segments = append(segments, parent)
		seen[pid] = true
		current = parent
	}
	// child branches off every segment in the path
	segmentIDs := map[string]bool{}
	parentIDs := []string{}
	for _, seg := range segments {
		id := asStr(seg.ID)
		segmentIDs[id] = true
		parentIDs = append(parentIDs, id)
	}
	childRows := []lineageRow{}
	for _, pid := range parentIDs {
		rows, err := db.Query(`SELECT id FROM sessions WHERE parent_session_id = ?`, pid)
		if err != nil {
			continue
		}
		var cids []string
		for rows.Next() {
			var cid string
			if rows.Scan(&cid) == nil {
				cids = append(cids, cid)
			}
		}
		rows.Close()
		parent, _ := fetchLineageRow(db, cols, pid)
		type childMeta struct {
			row   lineageRow
			order float64
		}
		var kids []childMeta
		for _, cid := range cids {
			if segmentIDs[cid] {
				continue
			}
			child, ok := fetchLineageRow(db, cols, cid)
			if !ok {
				continue
			}
			if continuationSession(parent, child) {
				// continuation outside the selected path → branched lineage
				manualReview = true
				continue
			}
			kids = append(kids, childMeta{row: child, order: asFloat(child.StartedAt)})
		}
		sort.SliceStable(kids, func(i, j int) bool { return kids[i].order > kids[j].order })
		for _, k := range kids {
			childRows = append(childRows, k.row)
		}
	}
	segmentsJSON := []any{}
	for idx, seg := range segments {
		role := "hidden_segment"
		if idx == 0 {
			role = "tip"
		}
		segmentsJSON = append(segmentsJSON, lineageReportRow(seg, role))
	}
	childrenJSON := []any{}
	for _, c := range childRows {
		childrenJSON = append(childrenJSON, lineageReportRow(c, "child_session"))
	}
	rootID := asStr(segments[len(segments)-1].ID)
	return map[string]any{
		"mutation":              false,
		"found":                 true,
		"session_id":            sid,
		"lineage_key":           rootID,
		"tip_session_id":        asStr(segments[0].ID),
		"total_segments":        len(segments),
		"materialized_segments": len(segments),
		"segments":              segmentsJSON,
		"children":              childrenJSON,
		"manual_review":         manualReview,
	}
}

// ── recovery audit ─────────────────────────────────────────────────────────

// msgCount returns the number of messages in a session JSON file, or -1.
func msgCount(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	var payload struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return -1
	}
	return len(payload.Messages)
}

// durableTombstoneMarksDeleted mirrors _durable_tombstone_marks_deleted_webui_session.
func durableTombstoneMarksDeleted(sessionDir, sid string) bool {
	if sid == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(sessionDir, sid+".json")); err == nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(sessionDir, "_deleted_webui_sessions.json"))
	if err != nil {
		return false
	}
	var tomb struct {
		Version int      `json:"version"`
		IDs     []string `json:"ids"`
	}
	if json.Unmarshal(raw, &tomb) != nil || tomb.Version != 1 {
		return false
	}
	for _, v := range tomb.IDs {
		if strings.TrimSpace(v) == sid {
			return true
		}
	}
	return false
}

// readIndexSessionIDs reads _index.json → set of session ids.
func readIndexSessionIDs(indexPath string) map[string]bool {
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return nil
	}
	var idx map[string]any
	if json.Unmarshal(raw, &idx) != nil {
		return nil
	}
	out := map[string]bool{}
	// shape: {"sessions": {"<sid>": {...}}} — tolerate flat maps too
	if sessions, ok := idx["sessions"].(map[string]any); ok {
		for k := range sessions {
			out[k] = true
		}
	}
	for k := range idx {
		if strings.HasSuffix(k, ".json") || len(k) == 20 { // raw sid-ish keys
			out[strings.TrimSuffix(k, ".json")] = true
		}
	}
	return out
}

// stateDBWebuiRowsWithSidecarState returns webui-origin rows missing their
// JSON sidecar, classified. Mirrors _read_state_db_missing_sidecar_rows core.
func stateDBWebuiRowsWithSidecarState(home, sessionDir string) (missing []map[string]any, tombstoned map[string]bool, emptyMsgs map[string]bool) {
	tombstoned = map[string]bool{}
	emptyMsgs = map[string]bool{}
	db, cols, err := stateDBSessions(home)
	if err != nil {
		return nil, tombstoned, emptyMsgs
	}
	defer db.Close()
	if !cols["id"] || !cols["source"] {
		return nil, tombstoned, emptyMsgs
	}
	rows, err := db.Query(`SELECT id FROM sessions WHERE source = 'webui'`)
	if err != nil {
		return nil, tombstoned, emptyMsgs
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		if rows.Scan(&sid) != nil || sid == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(sessionDir, sid+".json")); err == nil {
			continue
		}
		tomb := durableTombstoneMarksDeleted(sessionDir, sid)
		if tomb {
			tombstoned[sid] = true
			missing = append(missing, map[string]any{"id": sid, "_state_db_deleted_webui_tombstone": true})
			continue
		}
		// count messages in state.db for this sid
		n := -1
		if cols["message_count"] {
			_ = db.QueryRow(`SELECT COALESCE(message_count, 0) FROM sessions WHERE id = ?`, sid).Scan(&n)
		} else {
			var mc any
			_ = db.QueryRow(`SELECT 0 FROM sessions WHERE id = ?`, sid).Scan(&mc)
			n = 0
		}
		empty := n <= 0
		if empty {
			emptyMsgs[sid] = true
		}
		missing = append(missing, map[string]any{"id": sid})
	}
	return missing, tombstoned, emptyMsgs
}

func auditSessionRecovery(home, sessionDir string) map[string]any {
	if _, err := os.Stat(sessionDir); err != nil {
		return map[string]any{
			"status":  "ok",
			"summary": map[string]any{"ok": 0, "repairable": 0, "unsafe_to_repair": 0},
			"items":   []any{},
		}
	}
	items := []map[string]any{}
	liveIDs := map[string]bool{}

	entries, _ := os.ReadDir(sessionDir)
	livePaths := []string{}
	orphanBaks := []string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, "_") {
			livePaths = append(livePaths, filepath.Join(sessionDir, name))
			liveIDs[strings.TrimSuffix(name, ".json")] = true
		} else if strings.HasSuffix(name, ".json.bak") {
			live := strings.TrimSuffix(name, ".bak")
			if _, err := os.Stat(filepath.Join(sessionDir, live)); err != nil && !strings.HasPrefix(name, "_") {
				orphanBaks = append(orphanBaks, filepath.Join(sessionDir, name))
			}
		}
	}
	sort.Strings(livePaths)

	// 1. shrunken live with .bak (core check only: bak_count > live_count)
	for _, lp := range livePaths {
		bak := lp + ".bak"
		if _, err := os.Stat(bak); err != nil {
			continue
		}
		liveN := msgCount(lp)
		bakN := msgCount(bak)
		if bakN > liveN {
			items = append(items, map[string]any{
				"session_id":    strings.TrimSuffix(filepath.Base(lp), ".json"),
				"kind":          "shrunken_live",
				"category":      "repairable",
				"recommendation": "restore_from_bak",
				"live_messages": liveN,
				"bak_messages":  bakN,
			})
		}
	}

	missing, tombstoned, emptyMsgs := stateDBWebuiRowsWithSidecarState(home, sessionDir)
	tombBak := map[string]bool{}

	// 2. orphan .bak classification
	for _, bak := range orphanBaks {
		live := strings.TrimSuffix(bak, ".bak")
		sid := strings.TrimSuffix(filepath.Base(live), ".json")
		bakN := msgCount(bak)
		if bakN < 0 {
			items = append(items, map[string]any{
				"session_id": sid, "kind": "malformed_orphan_backup",
				"category": "unsafe_to_repair", "recommendation": "manual_review",
				"live_messages": -1, "bak_messages": bakN,
			})
		} else if tombstoned[sid] || durableTombstoneMarksDeleted(sessionDir, sid) {
			tombBak[sid] = true
			items = append(items, map[string]any{
				"session_id": sid, "kind": "state_db_deleted_webui_tombstone",
				"category": "unsafe_to_repair", "recommendation": "deleted_session_skipped",
				"live_messages": -1, "bak_messages": bakN,
			})
		} else if stateDBHasSession(home, sid) {
			items = append(items, map[string]any{
				"session_id": sid, "kind": "orphan_backup",
				"category": "repairable", "recommendation": "restore_from_bak",
				"live_messages": -1, "bak_messages": bakN,
			})
		} else {
			items = append(items, map[string]any{
				"session_id": sid, "kind": "orphan_backup_without_state_row",
				"category": "unsafe_to_repair", "recommendation": "manual_review",
				"live_messages": -1, "bak_messages": bakN,
			})
		}
	}

	// 3. index gaps
	if ids := readIndexSessionIDs(filepath.Join(sessionDir, "_index.json")); ids != nil {
		for sid := range ids {
			if !liveIDs[sid] {
				if tombstoned[sid] || durableTombstoneMarksDeleted(sessionDir, sid) {
					continue
				}
				items = append(items, map[string]any{
					"session_id": sid, "kind": "index_missing_file",
					"category": "repairable", "recommendation": "rebuild_index",
					"live_messages": -1, "bak_messages": -1,
				})
			}
		}
		for sid := range liveIDs {
			if !ids[sid] {
				items = append(items, map[string]any{
					"session_id": sid, "kind": "index_missing_entry",
					"category": "repairable", "recommendation": "rebuild_index",
					"live_messages": msgCount(filepath.Join(sessionDir, sid+".json")), "bak_messages": -1,
				})
			}
		}
	}

	// 4. state.db missing sidecars
	for _, row := range missing {
		sid := asStr(row["id"])
		if tombBak[sid] {
			continue // already emitted from orphan .bak branch (#5504)
		}
		if row["_state_db_deleted_webui_tombstone"] == true {
			items = append(items, map[string]any{
				"session_id": sid, "kind": "state_db_deleted_webui_tombstone",
				"category": "unsafe_to_repair", "recommendation": "deleted_session_skipped",
				"live_messages": -1, "bak_messages": -1,
			})
			continue
		}
		if emptyMsgs[sid] {
			items = append(items, map[string]any{
				"session_id": sid, "kind": "state_db_orphan_webui_row",
				"category": "unsafe_to_repair", "recommendation": "manual_review",
				"live_messages": -1, "bak_messages": -1,
			})
			continue
		}
		items = append(items, map[string]any{
			"session_id": sid, "kind": "state_db_missing_sidecar",
			"category": "repairable", "recommendation": "materialize_from_state_db",
			"live_messages": -1, "bak_messages": -1,
		})
	}

	summary := map[string]any{"ok": len(livePaths), "repairable": 0, "unsafe_to_repair": 0}
	for _, it := range items {
		switch it["category"] {
		case "repairable":
			summary["repairable"] = summary["repairable"].(int) + 1
		case "unsafe_to_repair":
			summary["unsafe_to_repair"] = summary["unsafe_to_repair"].(int) + 1
		}
	}
	overall := "ok"
	if summary["unsafe_to_repair"].(int) > 0 {
		overall = "needs_manual_review"
	} else if summary["repairable"].(int) > 0 {
		overall = "warn"
	}
	return map[string]any{"status": overall, "summary": summary, "items": items}
}

func stateDBHasSession(home, sid string) bool {
	db, cols, err := stateDBSessions(home)
	if err != nil || !cols["id"] {
		return false
	}
	defer db.Close()
	var one int
	return db.QueryRow("SELECT 1 FROM sessions WHERE id = ?", sid).Scan(&one) == nil
}

// webuiSessionsDir — HERMES_WEBUI_STATE_DIR/sessions or <home>/webui/sessions.
func webuiSessionsDir(home string) string {
	if env := os.Getenv("HERMES_WEBUI_STATE_DIR"); env != "" {
		return filepath.Join(env, "sessions")
	}
	return filepath.Join(home, "webui", "sessions")
}

// ── router ─────────────────────────────────────────────────────────────────

// Wave11Router serves the session read-only family.
func Wave11Router(r chi.Router, hermesHome string) {
	r.Get("/api/session/lineage/report", func(w http.ResponseWriter, req *http.Request) {
		sid := req.URL.Query().Get("session_id")
		if sid == "" {
			wave4WriteJSONErr(w, 400, "session_id required")
			return
		}
		wave4WriteJSON(w, 200, readSessionLineageReport(hermesHome, sid))
	})
	r.Get("/api/session/recovery/audit", func(w http.ResponseWriter, req *http.Request) {
		dir := webuiSessionsDir(hermesHome)
		wave4WriteJSON(w, 200, auditSessionRecovery(hermesHome, dir))
	})
}
