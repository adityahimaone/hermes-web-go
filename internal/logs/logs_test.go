package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempLog(t *testing.T, home, name, content string) {
	t.Helper()
	dir := filepath.Join(home, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadExactTailAndTruncatedIsFalse(t *testing.T) {
	home := t.TempDir()
	// 6 lines, tail=100 → all lines present; truncated false.
	writeTempLog(t, home, "agent.log", "a\nb\nc\nd\ne\nf\n")
	tail, err := Read(home, "agent", "100")
	if err != nil {
		t.Fatal(err)
	}
	if tail.File != "agent" || tail.Tail != 100 {
		t.Fatalf("file=%q tail=%d", tail.File, tail.Tail)
	}
	if got := len(tail.Lines); got != 6 {
		t.Fatalf("lines=%d, want 6", got)
	}
	if tail.Truncated {
		t.Fatal("want truncated=false for small file")
	}
	if tail.Mtime == nil {
		t.Fatal("mtime nil")
	}
}

func TestReadMissingFileReturnsHint(t *testing.T) {
	home := t.TempDir()
	tail, err := Read(home, "errors", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tail.Lines) != 0 {
		t.Fatalf("lines %d, want 0", len(tail.Lines))
	}
	if !strings.Contains(tail.Hint, "errors") {
		t.Fatalf("hint=%q", tail.Hint)
	}
}

func TestReadRejectsUnknownFile(t *testing.T) {
	_, err := Read(t.TempDir(), "nope", "")
	if err != ErrUnknownLogFile {
		t.Fatalf("err=%v, want ErrUnknownLogFile", err)
	}
}

func TestNormalizeTailOnlyWhitelisted(t *testing.T) {
	cases := map[string]int{"": 200, "100": 100, "500": 500, "7": 200, "1000": 1000, "abc": 200}
	for in, want := range cases {
		if got := NormalizeTail(in); got != want {
			t.Fatalf("NormalizeTail(%q)=%d, want %d", in, got, want)
		}
	}
}
