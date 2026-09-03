package httpserver

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/store"
)

// fakeRegistry is a streamRegistry stub used to test session/status liveness.
type fakeRegistry struct{ n int }

func (f fakeRegistry) Len() int { return f.n }

func familyTestRouter(t *testing.T, seed func(db *sql.DB)) (*chi.Mux, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if seed != nil {
		seed(db)
	}
	r := chi.NewRouter()
	SessionFamilyRouter(r, db, fakeRegistry{})
	return r, db
}

func doJSON(t *testing.T, r http.Handler, method, url string, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, url, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func seedFamilySession(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if err := store.ImportSession(db, store.SessionImport{
		ID: id, Title: "T", Workspace: "/tmp/w", Model: "gpt-4o",
		Messages: `[{"role":"user","content":"a"},{"role":"assistant","content":"b"}]`,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestSessionFamilyHandlers covers the nine Family-1 routes that are pure DB
// projections: status, usage, pin, archive, move, toolsets, draft, truncate,
// clear.
func TestSessionFamilyHandlers(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		r, _ := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, m := doJSON(t, r, http.MethodGet, "/api/session/status?session_id=s1", "")
		if code != 200 {
			t.Fatalf("status code = %d", code)
		}
		if m["session_id"] != "s1" || m["message_count"] != float64(2) {
			t.Fatalf("status = %#v", m)
		}
		// Python session_status() contract: no rev, no messages, no pinned.
		if _, ok := m["rev"]; ok {
			t.Fatalf("status must not expose rev: %#v", m)
		}
		if _, ok := m["messages"]; ok {
			t.Fatalf("status must not expose messages: %#v", m)
		}
		if _, ok := m["pinned"]; ok {
			t.Fatalf("status must not expose pinned: %#v", m)
		}
		// live registry: agent_running true
		_, db2 := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		rr := chi.NewRouter()
		SessionFamilyRouter(rr, db2, fakeRegistry{n: 1})
		_, m2 := doJSON(t, rr, http.MethodGet, "/api/session/status?session_id=s1", "")
		if m2["agent_running"] != true {
			t.Fatalf("agent_running = %#v", m2["agent_running"])
		}
	})

	t.Run("status_missing", func(t *testing.T) {
		r, _ := familyTestRouter(t, nil)
		if code, _ := doJSON(t, r, http.MethodGet, "/api/session/status?session_id=nope", ""); code != 404 {
			t.Fatalf("missing status code = %d", code)
		}
	})

	t.Run("usage", func(t *testing.T) {
		r, _ := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, m := doJSON(t, r, http.MethodGet, "/api/session/usage?session_id=s1", "")
		if code != 200 || m["total_tokens"] != float64(0) || m["model"] != "gpt-4o" {
			t.Fatalf("usage = %d %#v", code, m)
		}
	})

	t.Run("pin", func(t *testing.T) {
		r, db := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, m := doJSON(t, r, http.MethodPost, "/api/session/pin", `{"session_id":"s1","pinned":true}`)
		if code != 200 || m["ok"] != true {
			t.Fatalf("pin = %d %#v", code, m)
		}
		row, _ := store.GetSession(db, "s1")
		if row.Pinned != 1 {
			t.Fatalf("pinned = %d", row.Pinned)
		}
	})

	t.Run("pin_quota", func(t *testing.T) {
		r, _ := familyTestRouter(t, func(db *sql.DB) {
			for _, id := range []string{"a", "b", "c"} {
				seedFamilySession(t, db, id)
				if err := store.SetSessionFlag(db, id, "pinned", 1); err != nil {
					t.Fatal(err)
				}
			}
			seedFamilySession(t, db, "d")
		})
		code, _ := doJSON(t, r, http.MethodPost, "/api/session/pin", `{"session_id":"d"}`)
		if code != 400 {
			t.Fatalf("quota exceeded should be 400, got %d", code)
		}
	})

	t.Run("archive", func(t *testing.T) {
		r, db := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, m := doJSON(t, r, http.MethodPost, "/api/session/archive", `{"session_id":"s1"}`)
		if code != 200 || m["ok"] != true {
			t.Fatalf("archive = %d %#v", code, m)
		}
		row, _ := store.GetSession(db, "s1")
		if row.Archived != 1 {
			t.Fatalf("archived = %d", row.Archived)
		}
	})

	t.Run("move", func(t *testing.T) {
		r, db := familyTestRouter(t, func(db *sql.DB) {
			seedFamilySession(t, db, "s1")
			if err := store.ImportProject(db, store.ProjectImport{ID: "p1", Name: "P1"}); err != nil {
				t.Fatal(err)
			}
		})
		code, m := doJSON(t, r, http.MethodPost, "/api/session/move", `{"session_id":"s1","project_id":"p1"}`)
		if code != 200 || m["ok"] != true {
			t.Fatalf("move = %d %#v", code, m)
		}
		row, _ := store.GetSession(db, "s1")
		if row.ProjectID != "p1" {
			t.Fatalf("project = %q", row.ProjectID)
		}
	})

	t.Run("move_unknown_project", func(t *testing.T) {
		r, _ := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, _ := doJSON(t, r, http.MethodPost, "/api/session/move", `{"session_id":"s1","project_id":"ghost"}`)
		if code != 404 {
			t.Fatalf("unknown project move = %d", code)
		}
	})

	t.Run("toolsets", func(t *testing.T) {
		r, db := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, m := doJSON(t, r, http.MethodPost, "/api/session/toolsets", `{"session_id":"s1","toolsets":["web","code"]}`)
		if code != 200 || m["ok"] != true {
			t.Fatalf("toolsets = %d %#v", code, m)
		}
		row, _ := store.GetSession(db, "s1")
		if row.EnabledToolsets != `["web","code"]` {
			t.Fatalf("enabled_toolsets = %q", row.EnabledToolsets)
		}
	})

	t.Run("toolsets_bad_shape", func(t *testing.T) {
		r, _ := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, _ := doJSON(t, r, http.MethodPost, "/api/session/toolsets", `{"session_id":"s1","toolsets":"nope"}`)
		if code != 400 {
			t.Fatalf("bad toolsets shape = %d", code)
		}
		code, _ = doJSON(t, r, http.MethodPost, "/api/session/toolsets", `{"session_id":"s1","toolsets":[]}`)
		if code != 400 {
			t.Fatalf("empty toolsets = %d", code)
		}
		code, _ = doJSON(t, r, http.MethodPost, "/api/session/toolsets", `{"session_id":"s1","toolsets":[""]}`)
		if code != 400 {
			t.Fatalf("blank toolset string = %d", code)
		}
	})

	t.Run("draft_get_empty", func(t *testing.T) {
		r, _ := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, m := doJSON(t, r, http.MethodGet, "/api/session/draft?session_id=s1", "")
		if code != 200 {
			t.Fatalf("draft get = %d", code)
		}
		draft, _ := m["draft"].(map[string]any)
		if draft == nil {
			t.Fatalf("draft = %#v", m["draft"])
		}
	})

	t.Run("draft_save", func(t *testing.T) {
		r, db := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, m := doJSON(t, r, http.MethodPost, "/api/session/draft", `{"session_id":"s1","text":"hello world"}`)
		if code != 200 || m["ok"] != true {
			t.Fatalf("draft save = %d %#v", code, m)
		}
		draft, _ := m["draft"].(map[string]any)
		if draft["text"] != "hello world" {
			t.Fatalf("draft text = %#v", draft["text"])
		}
		row, _ := store.GetSession(db, "s1")
		if !strings.Contains(row.ComposerDraft, "hello world") {
			t.Fatalf("composer_draft = %q", row.ComposerDraft)
		}
	})

	t.Run("draft_cap_text", func(t *testing.T) {
		r, _ := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		long := strings.Repeat("x", maxDraftText+100)
		code, m := doJSON(t, r, http.MethodPost, "/api/session/draft", `{"session_id":"s1","text":`+jsonStr(long)+`}`)
		if code != 200 {
			t.Fatalf("draft cap = %d", code)
		}
		draft, _ := m["draft"].(map[string]any)
		if len(draft["text"].(string)) != maxDraftText {
			t.Fatalf("text len = %d, want %d", len(draft["text"].(string)), maxDraftText)
		}
	})

	t.Run("truncate", func(t *testing.T) {
		r, db := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, _ := doJSON(t, r, http.MethodPost, "/api/session/truncate", `{"session_id":"s1","keep_count":1}`)
		if code != 200 {
			t.Fatalf("truncate = %d", code)
		}
		row, _ := store.GetSession(db, "s1")
		var messages []any
		_ = json.Unmarshal([]byte(row.Messages), &messages)
		if len(messages) != 1 {
			t.Fatalf("messages len = %d", len(messages))
		}
	})

	t.Run("truncate_negative", func(t *testing.T) {
		r, _ := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, _ := doJSON(t, r, http.MethodPost, "/api/session/truncate", `{"session_id":"s1","keep_count":-1}`)
		if code != 400 {
			t.Fatalf("negative truncate = %d", code)
		}
	})

	t.Run("clear", func(t *testing.T) {
		r, db := familyTestRouter(t, func(db *sql.DB) { seedFamilySession(t, db, "s1") })
		code, _ := doJSON(t, r, http.MethodPost, "/api/session/clear", `{"session_id":"s1"}`)
		if code != 200 {
			t.Fatalf("clear = %d", code)
		}
		row, _ := store.GetSession(db, "s1")
		if row.Messages != "[]" {
			t.Fatalf("messages = %q", row.Messages)
		}
	})

	t.Run("missing_session", func(t *testing.T) {
		r, _ := familyTestRouter(t, nil)
		if code, _ := doJSON(t, r, http.MethodPost, "/api/session/pin", `{"session_id":"nope"}`); code != 404 {
			t.Fatalf("missing pin = %d", code)
		}
	})
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestSessionFamilyNativeNoProxyFallback ensures the Family-1 routes are routed
// natively (never fall through to the proxy) and that session/status reflects
// a live registry through the full server router.
func TestSessionFamilyNativeNoProxyFallback(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "webui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedFamilySession(t, db, "s1")

	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "proxy-fallback")
	})
	r := NewRouterWithData(".", proxy, db, t.TempDir())
	ts := httptest.NewServer(r)
	defer ts.Close()

	for _, u := range []string{
		"/api/session/status?session_id=s1",
		"/api/session/usage?session_id=s1",
	} {
		resp, err := http.Get(ts.URL + u)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || strings.Contains(string(body), "proxy-fallback") {
			t.Fatalf("%s = %d %q", u, resp.StatusCode, body)
		}
	}
}
