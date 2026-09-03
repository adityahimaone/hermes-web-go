package agentclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientCronMutationTranslatesRESTAndProfile(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"job":{"id":"abc"}}`))
	}))
	defer ts.Close()
	c := NewHTTPClient(ts.URL, "secret")
	status, body, err := c.CronMutation(context.Background(), "create", "", "jihyo", []byte(`{"name":"x"}`))
	if err != nil || status != http.StatusCreated || string(body) != `{"job":{"id":"abc"}}` {
		t.Fatalf("result=%d %s err=%v", status, body, err)
	}
	if gotMethod != http.MethodPost || gotPath != "/p/jihyo/api/jobs" || gotAuth != "Bearer secret" || gotBody != `{"name":"x"}` {
		t.Fatalf("request=%s %s %s %s", gotMethod, gotPath, gotAuth, gotBody)
	}
}

func TestHTTPClientCronMutationRejectsMalformedJSON(t *testing.T) {
	c := NewHTTPClient("http://127.0.0.1:1", "")
	if _, _, err := c.CronMutation(context.Background(), "create", "", "", []byte("{")); err == nil {
		t.Fatal("malformed payload accepted")
	}
}

func TestHTTPClientCronMutationActionPaths(t *testing.T) {
	cases := []struct {
		action, jobID, profile string
		wantMethod, wantPath   string
	}{
		{"create", "", "", http.MethodPost, "/api/jobs"},
		{"create", "", "jihyo", http.MethodPost, "/p/jihyo/api/jobs"},
		{"update", "abc", "", http.MethodPatch, "/api/jobs/abc"},
		{"update", "abc", "jihyo", http.MethodPatch, "/p/jihyo/api/jobs/abc"},
		{"delete", "abc", "", http.MethodDelete, "/api/jobs/abc"},
		{"pause", "abc", "", http.MethodPost, "/api/jobs/abc/pause"},
		{"resume", "abc", "", http.MethodPost, "/api/jobs/abc/resume"},
		{"run", "abc", "", http.MethodPost, "/api/jobs/abc/run"},
	}
	for _, tc := range cases {
		t.Run(tc.action+"/"+tc.profile, func(t *testing.T) {
			var gotMethod, gotPath string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			defer ts.Close()
			c := NewHTTPClient(ts.URL, "k")
			if _, _, err := c.CronMutation(context.Background(), tc.action, tc.jobID, tc.profile, nil); err != nil {
				t.Fatal(err)
			}
			if gotMethod != tc.wantMethod || gotPath != tc.wantPath {
				t.Fatalf("got %s %s, want %s %s", gotMethod, gotPath, tc.wantMethod, tc.wantPath)
			}
		})
	}
}
