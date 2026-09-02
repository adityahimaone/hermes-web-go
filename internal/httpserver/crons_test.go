package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hermes-web-go/internal/agentclient"
	"hermes-web-go/internal/approval"
)

func writeCronFixture(t *testing.T, home string) {
	t.Helper()
	cronDir := filepath.Join(home, "cron")
	if err := os.MkdirAll(filepath.Join(cronDir, "output", "job-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	jobs := `{"jobs":[{"id":"job-1","name":"n","enabled":true},{"id":"job-2","name":"p","enabled":false}]}`
	if err := os.WriteFile(filepath.Join(cronDir, "jobs.json"), []byte(jobs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cronDir, "output", "job-1", "20260101_000000.md"), []byte("run"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCronsList(t *testing.T) {
	home := t.TempDir()
	writeCronFixture(t, home)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/crons")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 2 {
		t.Fatalf("jobs = %#v", body.Jobs)
	}
}

func TestCronsOutput(t *testing.T) {
	home := t.TempDir()
	writeCronFixture(t, home)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/crons/output?job_id=job-1&limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Outputs []map[string]any `json:"outputs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Outputs) != 1 {
		t.Fatalf("outputs = %#v", body.Outputs)
	}
}

func TestCronsOutputRejectsTraversal(t *testing.T) {
	home := t.TempDir()
	writeCronFixture(t, home)
	r := NewRouterWithAgent("", nil, nil, "", fakeClientNoop{}, approval.NewStore(), WithHermesHome(home))
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/crons/output?job_id=..%2F..")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// fakeClientNoop satisfies agentclient.AgentClient for router construction.
type fakeClientNoop struct{}

func (fakeClientNoop) RunTurn(ctx context.Context, req agentclient.TurnRequest) (<-chan agentclient.TurnEvent, error) {
	panic("not used")
}

func (fakeClientNoop) Cancel(ctx context.Context, sessionID string) error {
	panic("not used")
}
