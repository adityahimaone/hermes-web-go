package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPasswordAndSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Password: "secret", StateDir: dir, SessionTTL: time.Hour}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !a.VerifyPassword("secret") || a.VerifyPassword("bad") {
		t.Fatal("password verification mismatch")
	}
	cookie, err := a.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	if !a.VerifySession(cookie) {
		t.Fatal("session should verify")
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions.json")); err != nil {
		t.Fatal(err)
	}
}

func TestAuthMiddlewareRejectsAndAllows(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Config{Password: "secret", StateDir: dir, SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	next := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	unauth := httptest.NewRecorder()
	next.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/data", nil))
	if unauth.Code != 401 {
		t.Fatalf("status = %d", unauth.Code)
	}
	cookie, _ := a.CreateSession()
	auth := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.AddCookie(&http.Cookie{Name: a.CookieName, Value: cookie})
	next.ServeHTTP(auth, req)
	if auth.Code != 204 {
		t.Fatalf("status = %d", auth.Code)
	}
}
