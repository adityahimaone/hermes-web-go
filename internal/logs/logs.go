// Package logs reads bounded tails of Hermes log files directly from disk.
//
// Mirrors Python WebUI `_handle_logs` (api/routes.py:11108): a hardcoded
// whitelist (no user-controlled filenames), a bounded read window, and a
// fixed set of tail sizes. The scheduler/log-writer ownership stays with the
// Hermes agent process; this package only reads.
package logs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrUnknownLogFile is returned when fileKey is not in the whitelist.
var ErrUnknownLogFile = errors.New("Unknown log file")

// whitelist mirrors Python _LOG_FILE_WHITELIST exactly.
var whitelist = map[string]string{
	"agent":   "agent.log",
	"errors":  "errors.log",
	"gateway": "gateway.log",
}

// allowed tail sizes mirror Python _LOG_TAIL_VALUES; anything else falls
// back to the default.
var tailValues = map[int]bool{100: true, 200: true, 500: true, 1000: true}

const (
	defaultTail = 200
	maxBytes    = 4 * 1024 * 1024 // Python _LOG_MAX_BYTES
)

// Tail is the response shape Python returns for /api/logs.
type Tail struct {
	File       string   `json:"file"`
	Tail       int      `json:"tail"`
	Lines      []string `json:"lines"`
	Truncated  bool     `json:"truncated"`
	TotalBytes int64    `json:"total_bytes"`
	Mtime      *float64 `json:"mtime"`
	Hint       string   `json:"hint"`
}

// NormalizeTail mirrors Python _normalize_logs_tail: parse or fall back to
// the default; only whitelisted sizes survive.
func NormalizeTail(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return defaultTail
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return defaultTail
	}
	if !tailValues[n] {
		return defaultTail
	}
	return n
}

// Read returns the bounded tail of <home>/logs/<whitelisted>. Missing file
// yields the Python "not found yet" shape (empty lines + hint, no error).
func Read(home, fileKey, rawTail string) (Tail, error) {
	filename, ok := whitelist[fileKey]
	if !ok {
		return Tail{}, ErrUnknownLogFile
	}
	tail := NormalizeTail(rawTail)
	out := Tail{
		File:  fileKey,
		Tail:  tail,
		Lines: []string{},
	}

	logDir := filepath.Join(home, "logs")
	logPath := filepath.Join(logDir, filename)

	// Defense in depth (mirrors Python): the final path must stay anchored
	// under the logs directory. Whitelist already pins filename, but keep
	// the check.
	cleanLogDir, _ := filepath.Abs(logDir)
	cleanLogPath, _ := filepath.Abs(logPath)
	if filepath.Dir(cleanLogPath) != cleanLogDir {
		return Tail{}, ErrUnknownLogFile
	}

	st, err := os.Stat(logPath)
	if err != nil || st.IsDir() {
		out.Hint = "Log file for " + fileKey + " not found yet."
		return out, nil
	}
	out.TotalBytes = st.Size()
	mtime := float64(st.ModTime().UnixNano()) / 1e9
	out.Mtime = &mtime
	out.Truncated = out.TotalBytes > maxBytes

	fh, err := os.Open(logPath)
	if err != nil {
		return Tail{}, err
	}
	defer fh.Close()

	readBytes := out.TotalBytes
	if readBytes > maxBytes {
		readBytes = maxBytes
		if _, err := fh.Seek(-maxBytes, io.SeekEnd); err != nil {
			return Tail{}, err
		}
	}
	buf := make([]byte, readBytes)
	if readBytes > 0 {
		if _, err := io.ReadFull(fh, buf); err != nil && err != io.ErrUnexpectedEOF {
			return Tail{}, err
		}
	}
	text := string(buf)
	// Strip null bytes like Python's decode replace would surface; keep
	// invalid UTF-8 replacement (Go already replaces on string conversion).
	text = strings.ReplaceAll(text, "\x00", "\uFFFD")
	lines := strings.Split(text, "\n")
	// Python splitlines drops trailing empty from final newline.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	// Handle \r remnants from \r\n split (mirror splitlines).
	for i, l := range lines {
		if strings.HasSuffix(l, "\r") {
			lines[i] = strings.TrimSuffix(l, "\r")
		}
	}
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	out.Lines = lines
	return out, nil
}
