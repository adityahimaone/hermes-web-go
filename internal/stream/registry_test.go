package stream

import (
	"testing"

	"hermes-web-go/internal/agentclient"
)

func TestRegistryLifecycle(t *testing.T) {
	reg := NewRegistry()
	id, ch := reg.Create()
	if id == "" || ch == nil {
		t.Fatalf("created stream = %q, %v", id, ch)
	}
	if got, ok := reg.Get(id); !ok || got != ch {
		t.Fatal("created stream not retrievable")
	}
	want := agentclient.TurnEvent{Type: agentclient.EventToken, Text: "hi"}
	if !reg.Publish(id, want) {
		t.Fatal("publish returned false")
	}
	if got := <-ch; got.Type != want.Type || got.Text != want.Text {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
	if !reg.Close(id) {
		t.Fatal("close returned false")
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel still open")
	}
	if !reg.Delete(id) {
		t.Fatal("delete returned false")
	}
	if reg.Publish(id, want) {
		t.Fatal("published to deleted stream")
	}
}

func TestRegistryMissingOperations(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Get("missing"); ok {
		t.Fatal("missing stream found")
	}
	if reg.Close("missing") || reg.Delete("missing") || reg.Publish("missing", agentclient.TurnEvent{}) {
		t.Fatal("missing operation succeeded")
	}
}
