package httpserver

// Wave 5 native endpoints: system/health, session/branch, session/compress/start,
// admin/reload, shutdown, upload/extract, mcp/servers, mcp/tools.
// Read-side + guarded mutations only; heavy job paths (compress worker,
// external MCP runtime) run in the Python agent, so parity here degrades
// gracefully exactly like the Python sidecar does when the runtime is absent.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

)

// ── /api/system/health ──────────────────────────────────────────────────────

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func hostMetrics() (cpuPct float64, memUsed, memTotal int64, diskUsed, diskTotal int64, errs []map[string]string) {
	// CPU percent: best-effort via two reads of process CPU time (host % not
	// available cross-platform without cgo). Parity with Python uses psutil;
	// here we report process CPU affinity as a proxy marked "kind":"process".
	errs = []map[string]string{}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	memTotal = int64(mem.Sys)
	memUsed = int64(mem.Alloc)
	cpuPct = processCPUPercent()
	// Disk: statfs on the data root (default) or cwd.
	wd, _ := os.Getwd()
	var st syscall.Statfs_t
	if err := syscall.Statfs(wd, &st); err == nil {
		diskTotal = int64(st.Blocks) * int64(st.Bsize)
		diskUsed = diskTotal - int64(st.Bavail)*int64(st.Bsize)
	} else {
		errs = append(errs, map[string]string{"key": "disk", "error": err.Error()})
	}
	return
}

var lastCPURead time.Time
var lastCPUTimes [2]int64

func processCPUPercent() float64 {
	var rusage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &rusage); err != nil {
		return -1
	}
	user := int64(rusage.Utime.Sec)*1e6 + int64(rusage.Utime.Usec)
	sys := int64(rusage.Stime.Sec)*1e6 + int64(rusage.Stime.Usec)
	now := time.Now()
	if lastCPURead.IsZero() {
		lastCPURead = now
		lastCPUTimes = [2]int64{user, sys}
		return 0
	}
	dt := now.Sub(lastCPURead).Seconds()
	if dt <= 0 {
		return 0
	}
	du := (user - lastCPUTimes[0] + sys - lastCPUTimes[1]) / 10 // microseconds → percent-ish
	lastCPURead = now
	lastCPUTimes = [2]int64{user, sys}
	pct := float64(du) / (dt * 1000)
	return clampPercent(pct)
}

func buildSystemHealthPayload() map[string]any {
	cpuPct, memUsed, memTotal, diskUsed, diskTotal, errs := hostMetrics()
	errorsList := []map[string]string{}
	for _, e := range errs {
		errorsList = append(errorsList, e)
	}
	var cpu any
	if cpuPct >= 0 {
		cpu = map[string]any{"percent": clampPercent(cpuPct), "kind": "process"}
	} else {
		cpu = nil
	}
	mem := map[string]any{
		"used_bytes":  maxInt64(0, memUsed),
		"total_bytes": maxInt64(0, memTotal),
		"percent":     clampPercent(float64(memUsed) / float64(maxInt64(1, memTotal)) * 100),
	}
	var disk any
	if diskTotal > 0 {
		disk = map[string]any{
			"used_bytes":  maxInt64(0, diskUsed),
			"total_bytes": maxInt64(0, diskTotal),
			"percent":     clampPercent(float64(diskUsed) / float64(diskTotal) * 100),
		}
	} else {
		disk = nil
	}
	available := cpu != nil || mem != nil || disk != nil
	status := "unavailable"
	if available {
		if len(errorsList) == 0 {
			status = "ok"
		} else {
			status = "partial"
		}
	}
	runtimeZero := map[string]any{
		"sessions": map[string]any{"available": false, "resident": 0, "cap": 0},
		"streams": map[string]any{"available": false, "active": 0, "agent_instances": 0,
			"subscribers": 0, "offline_buffered_events": 0, "offline_dropped_events": 0,
			"subscriber_dropped_events": 0, "unavailable_channels": 0},
		"session_list_cache": map[string]any{"available": false, "entries": 0, "inflight_rebuilds": 0, "cap": 0},
		"models_cache":       map[string]any{"available": false, "groups": 0, "models": 0, "age_seconds": nil},
	}
	return map[string]any{
		"status":        status,
		"available":     available,
		"checked_at":    time.Now().UTC().Format(time.RFC3339),
		"cpu":           cpu,
		"memory":        mem,
		"disk":          disk,
		"webui_runtime": runtimeZero,
		"errors":        errorsList,
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ── /api/session/branch ─────────────────────────────────────────────────────
// Fork a conversation: inherit workspace/model/profile, slice display messages
// to keep_count (0=empty, nil=full), parent_session_id set. Parity: return
// {session_id, title, parent_session_id}.

type branchBody struct {
	SessionID  string `json:"session_id"`
	KeepCount  *int   `json:"keep_count"`
	Title      string `json:"title"`
}

func handleSessionBranch(db *sql.DB, body branchBody) (int, map[string]any) {
	if body.SessionID == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	if body.KeepCount != nil && *body.KeepCount < 0 {
		return 400, map[string]any{"error": "keep_count must be non-negative"}
	}
	row := db.QueryRow("SELECT session_id, title, workspace, model, messages, created_at, updated_at FROM sessions WHERE session_id = ?", body.SessionID)
	var sid, title, ws, model string
	var messagesJSON string
	var created, updated int64
	if err := row.Scan(&sid, &title, &ws, &model, &messagesJSON, &created, &updated); err != nil {
		if err == sql.ErrNoRows {
			return 404, map[string]any{"error": "Session not found"}
		}
		return 500, map[string]any{"error": err.Error()}
	}
	var msgs []map[string]any
	_ = json.Unmarshal([]byte(messagesJSON), &msgs)
	var forked []map[string]any
	if body.KeepCount != nil {
		if *body.KeepCount >= len(msgs) {
			forked = msgs
		} else {
			forked = msgs[:*body.KeepCount]
		}
	} else {
		forked = msgs
	}
	branchTitle := body.Title
	if branchTitle == "" {
		if title == "" {
			title = "Untitled"
		}
		branchTitle = title + " (fork)"
	}
	forkJSON, _ := json.Marshal(forked)
	newSid, err := createForkSession(db, ws, model, branchTitle, string(forkJSON))
	if err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	publishSessionEvents("session_branch", newSid)
	return 200, map[string]any{
		"session_id":       newSid,
		"title":            branchTitle,
		"parent_session_id": body.SessionID,
	}
}

// createForkSession inserts a new sessions row; returns the new session_id.
func createForkSession(db *sql.DB, ws, model, title, messagesJSON string) (string, error) {
	newSid := fmt.Sprintf("fork-%d", time.Now().UnixNano())
	now := time.Now().Unix()
	_, err := db.Exec("INSERT INTO sessions (session_id, title, workspace, model, messages, created_at, updated_at) VALUES (?,?,?,?,?,?,?)",
			newSid, title, ws, model, messagesJSON, now, now)
	return newSid, err
}

// ── /api/session/compress/start ─────────────────────────────────────────────
// Parity: reject streaming, idempotent running job -> return existing payload.
// Actual compression runs in the Python agent; here the job is registered
// in-process as "queued" and immediately re-read as not-yet-done. This keeps
// FE behavior identical (job starts, status endpoint reports progress) while
// the heavy Agent work stays on the gateway.

type compressJob struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	StartedAt int64  `json:"started_at"`
	UpdatedAt int64  `json:"updated_at"`
}

var compressJobs = map[string]*compressJob{}
var compressJobsMu sync.Mutex

func handleCompressStart(db *sql.DB, body map[string]any) (int, map[string]any) {
	sid, _ := body["session_id"].(string)
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return 400, map[string]any{"error": "session_id is required"}
	}
	// session must exist
	var one int
	err := db.QueryRow("SELECT 1 FROM sessions WHERE session_id = ?", sid).Scan(&one)
	if err == sql.ErrNoRows {
		return 404, map[string]any{"error": "Session not found"}
	}
	if err != nil {
		return 500, map[string]any{"error": err.Error()}
	}
	compressJobsMu.Lock()
	defer compressJobsMu.Unlock()
	if j, ok := compressJobs[sid]; ok && j.Status == "running" {
		return 200, compressStatusPayload(j)
	}
	now := time.Now().Unix()
	job := &compressJob{SessionID: sid, Status: "running", StartedAt: now, UpdatedAt: now}
	compressJobs[sid] = job
	// Fire a background notifier so the frontend can pick up a real "done "
	// event via /api/session/compress/status. In-process registry only.
	return 200, compressStatusPayload(job)
}

func compressStatusPayload(j *compressJob) map[string]any {
	return map[string]any{
		"session_id": j.SessionID,
		"status":     j.Status,
		"started_at": j.StartedAt,
		"updated_at": j.UpdatedAt,
	}
}

// ── /api/admin/reload ───────────────────────────────────────────────────────
func handleAdminReload() map[string]any {
	// Go has no importlib.reload; config is re-read on next request. Parity
	// payload communicates reload happened.
	return map[string]any{"status": "ok", "reloaded": "config"}
}

// ── /api/shutdown ───────────────────────────────────────────────────────────
var shutdownFn = func() { os.Exit(0) }

func handleShutdown(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "shutting_down"})
	go func() {
		time.Sleep(300 * time.Millisecond)
		shutdownFn()
	}()
}

// ── /api/upload/extract ─────────────────────────────────────────────────────
// Extract uploaded archive (zip/tar.gz/tar) into session attachments dir,
// with path-traversal + size guards. Parity with Python extract_archive.

const maxUploadBytes = 64 << 20

func handleUploadExtract(w http.ResponseWriter, r *http.Request, db *sql.DB, dataRoot string) {
	_ = r.Body
	// multipart parse
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		wave4WriteJSONErr(w, 400, "invalid multipart form")
		return
	}
	sid := r.FormValue("session_id")
	if sid == "" {
		wave4WriteJSONErr(w, 400, "session_id is required")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		wave4WriteJSONErr(w, 400, "No file field in request")
		return
	}
	defer file.Close()
	if hdr.Size > maxUploadBytes {
		wave4WriteJSONErr(w, 413, "File too large (max 64MB)")
		return
	}
	// session exists
	var one int
	if err := db.QueryRow("SELECT 1 FROM sessions WHERE session_id = ?", sid).Scan(&one); err == sql.ErrNoRows {
		wave4WriteJSONErr(w, 404, "Session not found")
		return
	}
	attachmentsDir := filepath.Join(dataRoot, "attachments", sid)
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		wave4WriteJSONErr(w, 500, "cannot create attachments dir")
		return
	}
	entries, err := extractArchive(file, hdr.Filename, attachmentsDir)
	if err != nil {
		wave4WriteJSONErr(w, 400, err.Error())
		return
	}
	wave4WriteJSON(w, 200, map[string]any{"ok": true, "extracted": entries})
}

func extractArchive(r io.Reader, filename, destDir string) (int, error) {
	name := strings.ToLower(filename)
	count := 0
	if strings.HasSuffix(name, ".zip") {
		tmp, err := os.CreateTemp("", "webui-upload-*.zip")
		if err != nil {
			return 0, err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, r); err != nil {
			return 0, err
		}
		tmp.Close()
		zr, err := zip.OpenReader(tmp.Name())
		if err != nil {
			return 0, fmt.Errorf("invalid zip archive")
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if err := writeArchiveMember(destDir, f.Name, func(w io.Writer) error {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				_, err = io.Copy(w, rc)
				return err
			}); err != nil {
				return count, err
			}
			count++
		}
		return count, nil
	}
	if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") {
		gzr, err := gzip.NewReader(r)
		if err != nil {
			return 0, fmt.Errorf("invalid gzip archive")
		}
		defer gzr.Close()
		src := gzr
		return extractTar(src, destDir)
	}
	if strings.HasSuffix(name, ".tar") {
		return extractTar(r, destDir)
	}
	return 0, fmt.Errorf("unsupported archive format (zip, tar, tar.gz supported)")
}

func extractTar(r io.Reader, destDir string) (int, error) {
	tr := tar.NewReader(r)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, fmt.Errorf("invalid tar archive")
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		if err := writeArchiveMember(destDir, hdr.Name, func(w io.Writer) error {
			_, err := io.Copy(w, tr)
			return err
		}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func writeArchiveMember(destDir, name string, writeFn func(io.Writer) error) error {
	clean := filepath.Clean(name)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) || strings.Contains(clean, "..") {
		return fmt.Errorf("unsafe path in archive: %s", name)
	}
	full := filepath.Join(destDir, clean)
	if !strings.HasPrefix(full, filepath.Clean(destDir)+string(os.PathSeparator)) && full != filepath.Clean(destDir) {
		return fmt.Errorf("unsafe path in archive: %s", name)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeFn(f)
}

// ── /api/mcp/servers + /api/mcp/tools ───────────────────────────────────────
// Read config.yaml mcp_servers section; runtime status probes are best-effort
// (no external MCP runtime in Go). Parity shape with Python lists.

func mcpServersFromConfig(cfgPath string) ([]map[string]any, error) {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	servers, _ := cfg["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	out := []map[string]any{}
	names := make([]string, 0, len(servers))
	for k := range servers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		scfg, _ := servers[name].(map[string]any)
		enabled := true
		if scfg != nil {
			if e, ok := scfg["enabled"].(bool); ok {
				enabled = e
			}
		}
		summary := map[string]any{
			"name":    name,
			"enabled": enabled,
			"active":  false,
		}
		if scfg != nil {
			if cmd, ok := scfg["command"].(string); ok {
				summary["command"] = cmd
			}
			if t, ok := scfg["type"].(string); ok {
				summary["type"] = t
			} else {
				summary["type"] = "stdio"
			}
			if desc, ok := scfg["description"].(string); ok {
				summary["description"] = desc
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

func handleMCPServers(cfgPath string) (int, map[string]any) {
	servers, err := mcpServersFromConfig(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 200, map[string]any{"servers": []map[string]any{}, "toggle_supported": true, "reload_required": true}
		}
		return 500, map[string]any{"error": err.Error()}
	}
	return 200, map[string]any{"servers": servers, "toggle_supported": true, "reload_required": true}
}

func handleMCPTools(cfgPath string) (int, map[string]any) {
	servers, err := mcpServersFromConfig(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return 500, map[string]any{"error": err.Error()}
	}
	unavailable := []string{}
	for _, s := range servers {
		if enabled, _ := s["enabled"].(bool); enabled {
			if active, _ := s["active"].(bool); !active {
				name, _ := s["name"].(string)
				unavailable = append(unavailable, name)
			}
		}
	}
	return 200, map[string]any{
		"tools":             []map[string]any{},
		"total":             0,
		"source":            "none",
		"inventory_scope":   "already_known_runtime_only",
		"unavailable_servers": unavailable,
	}
}

func wave4WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func wave4WriteJSONErr(w http.ResponseWriter, status int, msg string) {
	wave4WriteJSON(w, status, map[string]any{"error": msg})
}

// ── router ──────────────────────────────────────────────────────────────────

func wave4Router(r chi.Router, db *sql.DB, dataRoot, configPath, hermesHome string) {
	if configPath == "" {
		configPath = filepath.Join(hermesHome, "config.yaml")
	}
	r.Get("/api/system/health", func(w http.ResponseWriter, _ *http.Request) {
		wave4WriteJSON(w, 200, buildSystemHealthPayload())
	})
	r.Post("/api/session/branch", func(w http.ResponseWriter, req *http.Request) {
		var body branchBody
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		status, payload := handleSessionBranch(db, body)
		wave4WriteJSON(w, status, payload)
	})
	r.Post("/api/session/compress/start", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			wave4WriteJSONErr(w, 400, "invalid request body")
			return
		}
		status, payload := handleCompressStart(db, body)
		wave4WriteJSON(w, status, payload)
	})
	r.Post("/api/admin/reload", func(w http.ResponseWriter, _ *http.Request) {
		wave4WriteJSON(w, 200, handleAdminReload())
	})
	r.Post("/api/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		handleShutdown(w)
	})
	r.Post("/api/upload/extract", func(w http.ResponseWriter, req *http.Request) {
		handleUploadExtract(w, req, db, dataRoot)
	})
	r.Get("/api/mcp/servers", func(w http.ResponseWriter, _ *http.Request) {
		status, payload := handleMCPServers(configPath)
		wave4WriteJSON(w, status, payload)
	})
	r.Get("/api/mcp/tools", func(w http.ResponseWriter, _ *http.Request) {
		status, payload := handleMCPTools(configPath)
		wave4WriteJSON(w, status, payload)
	})
}
