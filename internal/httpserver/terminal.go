package httpserver

// Wave 9 — native embedded terminal (PTY).
//
// POST /api/terminal/start  — spawn shell in session workspace (openpty,
//   new session, safe env allowlist, cap enforcement).
// POST /api/terminal/input   — write bytes to master fd (max 8KB).
// POST /api/terminal/resize  — ioctl TIOCSWINSZ rows/cols.
// POST /api/terminal/close   — kill process group + close fds.
// GET  /api/terminal/output  — SSE stream, bounded backlog + seq replay,
//   `terminal_output` / `terminal_closed` events.
//
// Parity with api/terminal.py: idempotent start (same ws/rows/cols reuse),
// restart replaces, reader goroutine fans out to N subscribers with per-sub
// drop-oldest, idle reaper closes unwatched terminals after grace.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/go-chi/chi/v5"
)

// ── terminal session state ─────────────────────────────────────────────────

const termIdleGrace = 15 * time.Minute
const termOutputMax = 4096
const termInputMax = 8192

type termItem struct {
	Seq     uint64
	Event   string
	Payload map[string]any
}

type terminalSession struct {
	SessionID string
	Workspace string
	Proc      *exec.Cmd
	Master    *os.File
	Rows, Cols int

	mu            sync.Mutex
	closed        bool
	nextSeq       uint64
	backlog       []termItem
	subs          map[chan termItem]struct{}
	unwatchedSince time.Time
	lastActivity   time.Time
}

var termMu sync.Mutex
var terminals = map[string]*terminalSession{}

func (t *terminalSession) isAlive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed
}

func (t *terminalSession) subCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.subs)
}

func (t *terminalSession) publish(event string, payload map[string]any) {
	t.mu.Lock()
	t.nextSeq++
	item := termItem{Seq: t.nextSeq, Event: event, Payload: payload}
	t.backlog = append(t.backlog, item)
	if len(t.backlog) > termOutputMax {
		t.backlog = append([]termItem(nil), t.backlog[len(t.backlog)-termOutputMax:]...)
	}
	t.lastActivity = time.Now()
	subs := make([]chan termItem, 0, len(t.subs))
	for ch := range t.subs {
		subs = append(subs, ch)
	}
	t.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- item:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- item:
			default:
			}
		}
	}
}

func (t *terminalSession) subscribe(afterSeq uint64) chan termItem {
	ch := make(chan termItem, 128)
	t.mu.Lock()
	for _, it := range t.backlog {
		if it.Seq > afterSeq {
			ch <- it
		}
	}
	t.subs[ch] = struct{}{}
	t.unwatchedSince = time.Time{}
	t.mu.Unlock()
	return ch
}

func (t *terminalSession) unsubscribe(ch chan termItem) {
	t.mu.Lock()
	delete(t.subs, ch)
	if len(t.subs) == 0 {
		t.unwatchedSince = time.Now()
	}
	t.mu.Unlock()
}

// ── spawn / teardown ───────────────────────────────────────────────────────

func safeEnv(cwd string, rows, cols int) []string {
	keys := []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG",
		"LC_ALL", "LC_CTYPE", "LC_MESSAGES", "LANGUAGE", "TZ", "TMPDIR", "TEMP",
		"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME"}
	env := []string{}
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	env = append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		fmt.Sprintf("COLUMNS=%d", cols),
		fmt.Sprintf("LINES=%d", rows),
		"PWD="+cwd,
		"HERMES_WEBUI_TERMINAL=1",
	)
	return env
}

func shellPath() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/bash"
}

func enforceTerminalCap(exclude string) {
	termMu.Lock()
	defer termMu.Unlock()
	if len(terminals) < 8 {
		return
	}
	var victims []*terminalSession
	now := time.Now()
	for sid, t := range terminals {
		if sid == exclude {
			continue
		}
		if t.isAlive() && t.subCount() == 0 && now.Sub(t.unwatchedSince) > 0 {
			victims = append(victims, t)
		}
	}
	if len(victims) == 0 {
		return
	}
	sort.Slice(victims, func(i, j int) bool { return victims[i].unwatchedSince.Before(victims[j].unwatchedSince) })
	closeterminal(victims[0].SessionID)
}

func startTerminal(sid, ws string, rows, cols int, restart bool) (*terminalSession, error) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	cwd, err := filepath.Abs(filepath.Clean(ws))
	if err != nil {
		return nil, fmt.Errorf("workspace is not a directory")
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("workspace is not a directory")
	}
	termMu.Lock()
	existing := terminals[sid]
	termMu.Unlock()
	if existing != nil && existing.isAlive() && !restart && existing.Workspace == cwd {
		existing.resize(rows, cols)
		return existing, nil
	}
	if existing != nil {
		closeterminal(sid)
	}
	enforceTerminalCap(sid)

	cmd := exec.Command(shellPath())
	cmd.Dir = cwd
	cmd.Env = safeEnv(cwd, rows, cols)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	t := &terminalSession{
		SessionID:      sid,
		Workspace:      cwd,
		Proc:           cmd,
		Master:         master,
		Rows:           rows,
		Cols:           cols,
		subs:           map[chan termItem]struct{}{},
		unwatchedSince: time.Now(),
		lastActivity:   time.Now(),
	}
	go t.readerLoop()
	termMu.Lock()
	terminals[sid] = t
	termMu.Unlock()
	return t, nil
}

func (t *terminalSession) readerLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := t.Master.Read(buf)
		if n > 0 {
			t.publish("terminal_output", map[string]any{
				"data": string(buf[:n]),
			})
		}
		if err != nil {
			break
		}
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.publish("terminal_closed", map[string]any{
		"session_id": t.SessionID,
		"exit":       true,
	})
}

func (t *terminalSession) write(data string) error {
	if len(data) > termInputMax {
		return fmt.Errorf("input too large")
	}
	if !t.isAlive() {
		return fmt.Errorf("terminal not running")
	}
	_, err := t.Master.Write([]byte(data))
	t.mu.Lock()
	t.lastActivity = time.Now()
	t.mu.Unlock()
	return err
}

func (t *terminalSession) resize(rows, cols int) {
	if rows < 1 || cols < 1 {
		return
	}
	t.mu.Lock()
	t.Rows, t.Cols = rows, cols
	t.mu.Unlock()
	_ = pty.Setsize(t.Master, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func closeterminal(sid string) bool {
	termMu.Lock()
	t, ok := terminals[sid]
	if ok {
		delete(terminals, sid)
	}
	termMu.Unlock()
	if !ok {
		return false
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	if t.Proc != nil && t.Proc.Process != nil {
		_ = syscall.Kill(-t.Proc.Process.Pid, syscall.SIGTERM)
		go func() {
			select {
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-t.Proc.Process.Pid, syscall.SIGKILL)
			case <-waitProc(t.Proc):
			}
		}()
	}
	if t.Master != nil {
		_ = t.Master.Close()
	}
	return true
}

func waitProc(cmd *exec.Cmd) chan struct{} {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return done
}

func startTerminalReaper() {
	go func() {
		for {
			time.Sleep(60 * time.Second)
			now := time.Now()
			termMu.Lock()
			victims := []string{}
			for sid, t := range terminals {
				if !t.isAlive() {
					victims = append(victims, sid)
					continue
				}
				t.mu.Lock()
				unw := t.unwatchedSince
				t.mu.Unlock()
				if !unw.IsZero() && now.Sub(unw) >= termIdleGrace {
					victims = append(victims, sid)
				}
			}
			termMu.Unlock()
			for _, sid := range victims {
				closeterminal(sid)
			}
		}
	}()
}

// ── handlers ───────────────────────────────────────────────────────────────

func handleTermStart(db *sql.DB, body map[string]any) (int, map[string]any) {
	sid, _ := body["session_id"].(string)
	var ws string
	if err := db.QueryRow("SELECT workspace FROM sessions WHERE session_id = ?", sid).Scan(&ws); err != nil {
		return 404, map[string]any{"error": "Session not found"}
	}
	rows, _ := body["rows"].(int)
	if rows < 1 {
		rows = 24
	}
	cols, _ := body["cols"].(int)
	if cols < 1 {
		cols = 80
	}
	restart, _ := body["restart"].(bool)
	term, err := startTerminal(sid, ws, rows, cols, restart)
	if err != nil {
		return 400, map[string]any{"error": err.Error()}
	}
	return 200, map[string]any{
		"ok":         true,
		"session_id": sid,
		"workspace":  term.Workspace,
		"running":    term.isAlive(),
	}
}

func handleTermInput(body map[string]any) (int, map[string]any) {
	sid, _ := body["session_id"].(string)
	data, _ := body["data"].(string)
	termMu.Lock()
	t, ok := terminals[sid]
	termMu.Unlock()
	if !ok || !t.isAlive() {
		return 404, map[string]any{"error": "terminal not running"}
	}
	if err := t.write(data); err != nil {
		return 400, map[string]any{"error": err.Error()}
	}
	return 200, map[string]any{"ok": true}
}

func handleTermResize(body map[string]any) (int, map[string]any) {
	sid, _ := body["session_id"].(string)
	rows, _ := body["rows"].(int)
	cols, _ := body["cols"].(int)
	termMu.Lock()
	t, ok := terminals[sid]
	termMu.Unlock()
	if !ok {
		return 404, map[string]any{"error": "terminal not running"}
	}
	t.resize(rows, cols)
	return 200, map[string]any{"ok": true}
}

func handleTermClose(body map[string]any) (int, map[string]any) {
	sid, _ := body["session_id"].(string)
	closed := closeterminal(sid)
	return 200, map[string]any{"ok": true, "closed": closed}
}

func handleTermOutput(w http.ResponseWriter, req *http.Request) {
	sid := req.URL.Query().Get("session_id")
	if sid == "" {
		wave4WriteJSONErr(w, 400, "session_id required")
		return
	}
	afterSeq := uint64(0)
	if h := req.Header.Get("Last-Event-ID"); h != "" {
		var n int
		if _, err := fmt.Sscanf(h, "%d", &n); err == nil && n > 0 {
			afterSeq = uint64(n)
		}
	}
	termMu.Lock()
	t, ok := terminals[sid]
	termMu.Unlock()
	if !ok {
		wave4WriteJSONErr(w, 404, "terminal not running")
		return
	}
	ch := t.subscribe(afterSeq)
	defer t.unsubscribe(ch)
	flusher := sseHeaders(w)
	if flusher == nil {
		wave4WriteJSONErr(w, 500, "streaming unsupported")
		return
	}
	for {
		select {
		case <-req.Context().Done():
			return
		case item := <-ch:
			if !writeSSEFrame(w, flusher, item.Event, item.Payload) {
				return
			}
		case <-time.After(30 * time.Second):
			if !writeSSEKeepalive(w, flusher) {
				return
			}
		}
	}
}

// ── router ──────────────────────────────────────────────────────────────────

func TerminalRouter(r chi.Router, db *sql.DB) {
	startTerminalReaper()
	r.Post("/api/terminal/start", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleTermStart(db, body)
		wave4WriteJSON(w, code, payload)
	})
	r.Post("/api/terminal/input", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleTermInput(body)
		wave4WriteJSON(w, code, payload)
	})
	r.Post("/api/terminal/resize", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleTermResize(body)
		wave4WriteJSON(w, code, payload)
	})
	r.Post("/api/terminal/close", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		code, payload := handleTermClose(body)
		wave4WriteJSON(w, code, payload)
	})
	r.Get("/api/terminal/output", func(w http.ResponseWriter, req *http.Request) {
		handleTermOutput(w, req)
	})
}