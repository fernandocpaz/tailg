package kube

import (
	"context"
	"testing"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestSendLogEventStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sendLogEvent(ctx, make(chan core.LogEvent), core.LogEvent{Message: "blocked"}) {
		t.Fatal("event was sent after cancellation")
	}
}

func TestSendLogEventDelivers(t *testing.T) {
	events := make(chan core.LogEvent, 1)
	want := core.LogEvent{Pod: "pod-1", Container: "app", Message: "ready"}
	if !sendLogEvent(context.Background(), events, want) {
		t.Fatal("event was not delivered")
	}
	if got := <-events; got.Pod != want.Pod || got.Container != want.Container || got.Message != want.Message {
		t.Fatalf("event = %#v, want %#v", got, want)
	}
}
