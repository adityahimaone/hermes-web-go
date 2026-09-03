package stream

import (
	"testing"

	"hermes-web-go/internal/agentclient"
)

func TestJournalBroadcastsAndReplaysInSequence(t *testing.T) {
	j := NewJournal(8)
	replay, live, cancel := j.Subscribe(0)
	defer cancel()

	first := j.Publish(agentclient.TurnEvent{Type: agentclient.EventToken, Text: "one"})
	second := j.Publish(agentclient.TurnEvent{Type: agentclient.EventDone})
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("sequence = %d, %d; want 1, 2", first.Seq, second.Seq)
	}
	if len(replay) != 0 {
		t.Fatalf("initial replay = %#v", replay)
	}
	if got := <-live; got.Seq != 1 {
		t.Fatalf("live first sequence = %d", got.Seq)
	}
	if got := <-live; got.Seq != 2 {
		t.Fatalf("live second sequence = %d", got.Seq)
	}

	replay, _, cancelReplay := j.Subscribe(1)
	defer cancelReplay()
	if len(replay) != 1 || replay[0].Seq != 2 {
		t.Fatalf("replay after 1 = %#v", replay)
	}
}

func TestJournalFanoutAndTerminalReplay(t *testing.T) {
	j := NewJournal(8)
	_, first, cancelFirst := j.Subscribe(0)
	defer cancelFirst()
	_, second, cancelSecond := j.Subscribe(0)
	defer cancelSecond()

	j.Publish(agentclient.TurnEvent{Type: agentclient.EventToken, Text: "shared"})
	j.Finish(agentclient.TurnEvent{Type: agentclient.EventDone})

	for name, ch := range map[string]<-chan Event{"first": first, "second": second} {
		if got := <-ch; got.Event.Text != "shared" || got.Seq != 1 {
			t.Fatalf("%s token = %#v", name, got)
		}
		if got := <-ch; got.Event.Type != agentclient.EventDone || got.Seq != 2 {
			t.Fatalf("%s terminal = %#v", name, got)
		}
		if _, ok := <-ch; ok {
			t.Fatalf("%s channel still open", name)
		}
	}

	replay, live, cancel := j.Subscribe(0)
	defer cancel()
	if len(replay) != 2 || replay[0].Seq != 1 || replay[1].Seq != 2 {
		t.Fatalf("terminal replay = %#v", replay)
	}
	if live != nil {
		t.Fatal("terminal journal returned live channel")
	}
}

func TestJournalRetentionBoundsReplay(t *testing.T) {
	j := NewJournal(2)
	j.Publish(agentclient.TurnEvent{Type: agentclient.EventToken, Text: "one"})
	j.Publish(agentclient.TurnEvent{Type: agentclient.EventToken, Text: "two"})
	j.Publish(agentclient.TurnEvent{Type: agentclient.EventToken, Text: "three"})

	replay, _, cancel := j.Subscribe(0)
	defer cancel()
	if len(replay) != 2 || replay[0].Event.Text != "two" || replay[1].Event.Text != "three" {
		t.Fatalf("bounded replay = %#v", replay)
	}
}
