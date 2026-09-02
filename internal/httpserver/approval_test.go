package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"hermes-web-go/internal/approval"
)

func newApprovalRouter(st *approval.Store) http.Handler {
	r := chi.NewRouter()
	r.Use(Recover)
	ApprovalRouter(r, st)
	return r
}

func TestApprovalRouteNativeNotProxied(t *testing.T) {
	// A proxy catch-all would 404 a path it doesn't recognize as native. If
	// approval is served by Go, /api/approval/pending must answer 200 (with a
	// valid session_id) even though a proxy handler is registered.
	st := approval.NewStore()
	st.Submit(approval.PendingApproval{ID: "p1", SessionID: "nat", Command: "ls", PatternKeys: []string{"ls"}})
	r := NewRouterWithAgent("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}), nil, "", fakeClientNoop{}, st)
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL + "/api/approval/pending?session_id=nat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approval pending status = %d, want 200 (native, not proxied)", resp.StatusCode)
	}
}

func TestCronsRouteNativeNotProxied(t *testing.T) {
	st := approval.NewStore()
	r := NewRouterWithAgent("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}), nil, "", fakeClientNoop{}, st)
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL + "/api/crons")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// /api/crons is native (file-backed); without a proxy, a non-native route
	// would 404. Crons lists from Hermes home — just assert it's handled, not
	// proxied, by the 200-vs-404 distinction with both registered.
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("crons proxied/404, want native handling")
	}
}

func TestApprovalRespondInvalidChoice(t *testing.T) {
	ts := httptest.NewServer(newApprovalRouter(approval.NewStore()))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/approval/respond", "application/json", bytes.NewBufferString(`{"session_id":"appr1","choice":"INVALID"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid choice status = %d", resp.StatusCode)
	}
}

func TestApprovalRespondMissingSession(t *testing.T) {
	ts := httptest.NewServer(newApprovalRouter(approval.NewStore()))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/approval/respond", "application/json", bytes.NewBufferString(`{"choice":"deny"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing session status = %d", resp.StatusCode)
	}
}

func TestApprovalSubmitThenRespond(t *testing.T) {
	s := approval.NewStore()
	s.Submit(approval.PendingApproval{ID: "a1", SessionID: "appr2", Command: "rm x", PatternKeys: []string{"rm"}})
	ts := httptest.NewServer(newApprovalRouter(s))
	defer ts.Close()

	// pending returns the submitted entry (head of queue)
	resp, err := http.Get(ts.URL + "/api/approval/pending?session_id=appr2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pending status = %d", resp.StatusCode)
	}
	var body struct {
		Pending      map[string]any `json:"pending"`
		PendingCount int            `json:"pending_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Pending["command"] != "rm x" {
		t.Fatalf("pending command = %v", body.Pending["command"])
	}
	if body.PendingCount != 1 {
		t.Fatalf("pending_count = %d", body.PendingCount)
	}

	// respond "session" approves all pattern_keys and clears the queue
	resp2, err := http.Post(ts.URL+"/api/approval/respond", "application/json", bytes.NewBufferString(`{"session_id":"appr2","choice":"session"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("respond status = %d", resp2.StatusCode)
	}
	var respBody struct {
		OK     bool   `json:"ok"`
		Choice string `json:"choice"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&respBody); err != nil {
		t.Fatal(err)
	}
	if !respBody.OK || respBody.Choice != "session" {
		t.Fatalf("respond body = %+v", respBody)
	}
	if !s.IsApproved("appr2", "rm") {
		t.Fatal("pattern_key not approved")
	}
	resp3, err := http.Get(ts.URL + "/api/approval/pending?session_id=appr2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	var body3 struct {
		Pending      map[string]any `json:"pending"`
		PendingCount int            `json:"pending_count"`
	}
	if err := json.NewDecoder(resp3.Body).Decode(&body3); err != nil {
		t.Fatal(err)
	}
	if body3.Pending != nil || body3.PendingCount != 0 {
		t.Fatalf("pending not cleared: %+v", body3)
	}
}
