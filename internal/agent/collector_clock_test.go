package agent

import (
	"context"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

type freshLogClient struct{ fakeKubernetesClient }

func (freshLogClient) Snapshot(ctx context.Context, item core.InventoryItem, _ kube.LogOptions) ([]core.LogEvent, error) {
	next := time.Now().Truncate(time.Second).Add(time.Second + 30*time.Millisecond)
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	return []core.LogEvent{{Pod: item.Pod, Container: item.Container, Message: "ERR upstream connection refused", ObservedAt: time.Now().UTC()}}, nil
}

func TestCollectorIncludesErrorsEmittedAfterCollectionStarts(t *testing.T) {
	item := core.InventoryItem{Pod: "api", Container: "api"}
	report, err := (Collector{Client: freshLogClient{}}).Collect(context.Background(), []core.InventoryItem{item}, CollectOptions{Mode: ModeIssues, Limits: Limits{Tail: 100, MaxLines: 100, MaxIssues: 10}})
	if err != nil || len(report.Issues) != 1 || report.Issues[0].Count != 1 {
		t.Fatalf("fresh error was excluded as a future bucket: %v, %+v", err, report.Summary)
	}
}
