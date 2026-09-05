package httpserver

import "testing"

func sidecarAssistant(content string, dur float64) map[string]any {
	return map[string]any{"role": "assistant", "content": content, "_turnDuration": dur, "_usedModel": "codex"}
}

func stateAssistant(content string) map[string]any {
	return map[string]any{"role": "assistant", "content": content}
}

// After a reload GET /api/session reconciles the agent's state.db transcript
// with the WebUI sidecar. Only the sidecar carries _turnDuration (state.db
// never does), and EVERY settled turn needs it to render "Processed in Xs" —
// dropping it on earlier turns degraded them to a bare "Processed" label.
func TestReconcileCarriesTurnMetaToEveryAssistant(t *testing.T) {
	stateRows := []map[string]any{
		{"role": "user", "content": "hellow"},
		stateAssistant("Hai Adit!"),
		{"role": "user", "content": "hello"},
		stateAssistant("Hello hello!"),
	}
	sidecar := []map[string]any{
		{"role": "user", "content": "hellow"},
		sidecarAssistant("Hai Adit!", 14.3),
		{"role": "user", "content": "hello"},
		sidecarAssistant("Hello hello!", 20.8),
	}
	merged := reconcileSessionMessages(sidecar, stateRows)
	assists := 0
	for _, m := range merged {
		if m["role"] != "assistant" {
			continue
		}
		assists++
		if m["_turnDuration"] == nil {
			t.Fatalf("assistant %q lost _turnDuration after reconcile", m["content"])
		}
	}
	if assists != 2 {
		t.Fatalf("got %d assistants, want 2", assists)
	}
}

// state.db copy's content can drift (provider whitespace etc.) — the sidecar
// meta must still land on the right turn via order-preserving fallback.
func TestReconcileCarriesTurnMetaPositionalFallback(t *testing.T) {
	stateRows := []map[string]any{
		{"role": "user", "content": "q1"},
		stateAssistant("answer one  "),
	}
	sidecar := []map[string]any{
		{"role": "user", "content": "q1"},
		sidecarAssistant("answer one", 7.5),
	}
	merged := reconcileSessionMessages(sidecar, stateRows)
	found := false
	for _, m := range merged {
		if m["role"] == "assistant" {
			found = true
			if m["_turnDuration"] == nil {
				t.Fatalf("assistant %q missing _turnDuration", m["content"])
			}
		}
	}
	if !found {
		t.Fatal("no assistant in merged transcript")
	}
}

// An assistant that already carries a duration must never be clobbered.
func TestReconcileKeepsExistingTurnDuration(t *testing.T) {
	stateRows := []map[string]any{stateAssistant("same content")}
	sidecar := []map[string]any{sidecarAssistant("same content", 9.1)}
	merged := reconcileSessionMessages(sidecar, stateRows)
	for _, m := range merged {
		if m["role"] == "assistant" && m["_turnDuration"] != nil && m["_turnDuration"].(float64) != 9.1 {
			t.Fatalf("unexpected duration %v", m["_turnDuration"])
		}
	}
}

// No sidecar timing at all → carry is a no-op (no fabricated durations).
func TestReconcileCarryNoopWithoutSidecarTiming(t *testing.T) {
	stateRows := []map[string]any{stateAssistant("answer")}
	sidecar := []map[string]any{{"role": "user", "content": "q"}}
	merged := reconcileSessionMessages(sidecar, stateRows)
	for _, m := range merged {
		if m["role"] == "assistant" && m["_turnDuration"] != nil {
			t.Fatalf("fabricated duration %v", m["_turnDuration"])
		}
	}
}
