package core

import (
	"context"
	"errors"
    "fmt"
    "sync/atomic"
	"sync"
	"testing"
	"time"
)

func TestFollowStreamsDoesNotRetryCompletedJob(t *testing.T) {
    for _, initiallyComplete := range []bool{true, false} {
        t.Run(fmt.Sprintf("initiallyComplete=%t", initiallyComplete), func(t *testing.T) {
            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()

            var started atomic.Int32
            var completionObserved atomic.Bool
            completionObserved.Store(initiallyComplete)
            attempts := make(chan int32, 3)
            item := InventoryItem{Pod: "cronjob-auditlistener-job", Container: "worker", Completed: initiallyComplete}
            finished := make(chan struct{})

            go func() {
                FollowStreams(ctx, StreamConfig{
                    Items: []InventoryItem{item},
                    RefreshInterval: 10 * time.Millisecond,
                    Inventory: func(context.Context) ([]InventoryItem, error) {
                        copyOfItem := item
                        copyOfItem.Completed = completionObserved.Load()
                        return []InventoryItem{copyOfItem}, nil
                    },
                    Stream: func(context.Context, InventoryItem, *LogCursor, chan<- LogEvent) error {
                        attempt := started.Add(1)
                        completionObserved.Store(true)
                        attempts <- attempt
                        return nil // kubectl -f naturally exits at Job completion.
                    },
                }, make(chan LogEvent, 10))
                close(finished)
            }()

            select {
            case count := <-attempts:
                if count != 1 {
                    t.Fatalf("initial stream attempt = %d", count)
                }
            case <-time.After(time.Second):
                t.Fatal("first stream never started")
            }
            // The backoff is 250ms. Wait beyond that to detect an accidental retry.
            select {
            case count := <-attempts:
                t.Fatalf("completed Job was followed again (attempt %d)", count)
            case <-time.After(550 * time.Millisecond):
            }
            cancel()
            select {
            case <-finished:
            case <-time.After(time.Second):
                t.Fatal("stream manager did not stop")
            }
        })
    }
}

func TestFollowStreamsReplacesRuntimeAndIgnoresOldCompletion(t *testing.T) {
	for _, change := range []struct {
		name string
		item InventoryItem
	}{
		{"restart", InventoryItem{Pod: "api", Container: "app", PodUID: "uid", ContainerID: "one", RestartCount: 1}},
		{"container", InventoryItem{Pod: "api", Container: "app", PodUID: "uid", ContainerID: "two"}},
		{"pod UID", InventoryItem{Pod: "api", Container: "app", PodUID: "new", ContainerID: "one"}},
	} {
		t.Run(change.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			initial := InventoryItem{Pod: "api", Container: "app", PodUID: "uid", ContainerID: "one"}
			var mu sync.Mutex
			current := []InventoryItem{initial}
			type start struct {
				item   InventoryItem
				cursor *LogCursor
				resume LogReplay
			}
			started := make(chan start, 10)
			oldCancelled, releaseOld := make(chan struct{}), make(chan struct{})
			finished := make(chan struct{})
			config := StreamConfig{
				Items: current, RefreshInterval: 5 * time.Millisecond,
				Inventory: func(context.Context) ([]InventoryItem, error) {
					mu.Lock()
					defer mu.Unlock()
					return append([]InventoryItem(nil), current...), nil
				},
				Stream: func(ctx context.Context, item InventoryItem, cursor *LogCursor, _ chan<- LogEvent) error {
					resume := cursor.Resume()
					cursor.Observe(LogEvent{Message: "startup", ObservedAt: time.Now()})
					started <- start{item, cursor, resume}
					<-ctx.Done()
					if item == initial {
						close(oldCancelled)
						<-releaseOld
					}
					return ctx.Err()
				},
			}
			go func() { FollowStreams(ctx, config, make(chan LogEvent, 10)); close(finished) }()
			first := awaitStart(t, started)
			mu.Lock()
			current = []InventoryItem{change.item}
			mu.Unlock()
			second := awaitStart(t, started)
			if second.item != change.item || first.cursor == second.cursor || !second.resume.Since.IsZero() {
				t.Fatalf("replacement reused stale runtime/cursor: %+v", second)
			}
			select {
			case <-oldCancelled:
			case <-time.After(time.Second):
				t.Fatal("old stream was not cancelled")
			}
			close(releaseOld)
			select {
			case extra := <-started:
				t.Fatalf("old completion restarted new stream: %+v", extra)
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("streams did not stop")
			}
		})
	}
}

func awaitStart[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not start")
		var zero T
		return zero
	}
}

func TestFollowStreamsRetryPreservesCursorAndInventoryErrorPreservesStreams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan *LogCursor, 10)
	finished := make(chan struct{})
	item := InventoryItem{Pod: "api", Container: "app"}
	errSeen := make(chan error, 10)
	go func() {
		FollowStreams(ctx, StreamConfig{
			Items: []InventoryItem{item}, RefreshInterval: 10 * time.Millisecond,
			Inventory: func(context.Context) ([]InventoryItem, error) { return nil, errors.New("API unavailable") },
			Updated: func(_ []InventoryItem, err error) {
				if err != nil {
					select {
					case errSeen <- err:
					default:
					}
				}
			},
			Stream: func(ctx context.Context, _ InventoryItem, cursor *LogCursor, _ chan<- LogEvent) error {
				fresh := cursor.Resume().Since.IsZero()
				cursor.Observe(LogEvent{Message: "one", ObservedAt: time.Now()})
				started <- cursor
				if fresh {
					return errors.New("connection ended")
				}
				<-ctx.Done()
				return ctx.Err()
			},
		}, make(chan LogEvent, 10))
		close(finished)
	}()
	first, second := awaitStart(t, started), awaitStart(t, started)
	if first != second {
		t.Fatal("network reconnect reset cursor")
	}
	awaitStart(t, errSeen)
	select {
	case <-started:
		t.Fatal("inventory failure restarted a healthy stream")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	awaitStart(t, finished)
}

func TestFollowStreamsReconcilesNewPodsAndEmptyRolloutGap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	current := []InventoryItem{{Pod: "old", Container: "api"}}
	started, stopped := make(chan string, 10), make(chan string, 10)
	finished := make(chan struct{})
	go func() {
		FollowStreams(ctx, StreamConfig{
			Items: current, RefreshInterval: 5 * time.Millisecond,
			Inventory: func(context.Context) ([]InventoryItem, error) {
				mu.Lock()
				defer mu.Unlock()
				return append([]InventoryItem(nil), current...), nil
			},
			Stream: func(ctx context.Context, item InventoryItem, _ *LogCursor, _ chan<- LogEvent) error {
				started <- item.Pod
				<-ctx.Done()
				stopped <- item.Pod
				return ctx.Err()
			},
		}, make(chan LogEvent, 10))
		close(finished)
	}()
	if awaitStart(t, started) != "old" {
		t.Fatal("initial pod missing")
	}
	mu.Lock()
	current = nil
	mu.Unlock()
	if awaitStart(t, stopped) != "old" {
		t.Fatal("removed pod was not cancelled")
	}
	mu.Lock()
	current = []InventoryItem{{Pod: "new", Container: "api"}, {Pod: "replica", Container: "api"}}
	mu.Unlock()
	got := map[string]bool{awaitStart(t, started): true, awaitStart(t, started): true}
	if !got["new"] || !got["replica"] {
		t.Fatalf("new inventory not followed: %v", got)
	}
	cancel()
	awaitStart(t, finished)
}

func TestInventoryItemRuntimeMetadataEnrichment(t *testing.T) {
	old := InventoryItem{Pod: "api", Container: "app", PodUID: "uid"}
	new := old
	new.ContainerID = "one"
	if new.Replaced(old) {
		t.Fatal("first container ID should not count as restart")
	}
	missing := new
	missing.ContainerID = ""
	if missing.Replaced(new) {
		t.Fatal("temporarily absent container ID should not count as restart")
	}
}
