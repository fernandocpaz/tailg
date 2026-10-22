package core

import (
	"context"
	"sync"
	"time"
)

// StreamConfig manages live logs independently of the rendering mode.
type StreamConfig struct {
	Items           []InventoryItem
	RefreshInterval time.Duration
	Stream          func(context.Context, InventoryItem, *LogCursor, chan<- LogEvent) error
	Inventory       func(context.Context) ([]InventoryItem, error)
	Updated         func([]InventoryItem, error)
}

type managedStream struct {
	item       InventoryItem
	cancel     context.CancelFunc
	generation uint64
	attempt    int
	cursor     *LogCursor
}

type managedStreamDone struct {
	key        string
	generation uint64
	attempt    int
	lifetime   time.Duration
}

type managedStreamRetry struct {
	key        string
	generation uint64
}

const (
	streamRetryBase  = 250 * time.Millisecond
	streamRetryLimit = 5 * time.Second
	stableStreamTime = 30 * time.Second
)

func streamRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := streamRetryBase
	for step := 1; step < attempt && delay < streamRetryLimit; step++ {
		delay *= 2
	}
	return min(delay, streamRetryLimit)
}

// FollowStreams reconciles inventory, restarts changed containers, and retries
// ended streams. It closes events after every stream has stopped.
func FollowStreams(ctx context.Context, config StreamConfig, events chan<- LogEvent) {
	defer close(events)
	if config.Stream == nil {
		return
	}

	done := make(chan managedStreamDone)
	retries := make(chan managedStreamRetry)
	active := map[string]*managedStream{}
	wanted := map[string]InventoryItem{}
	var streams sync.WaitGroup
	var generation uint64

	start := func(item InventoryItem, attempt int) {
		generation++
		streamCtx, cancel := context.WithCancel(ctx)
		cursor := &LogCursor{}
		if previous := active[item.Key()]; previous != nil && !item.Replaced(previous.item) {
			cursor = previous.cursor
		}
		handle := &managedStream{item: item, cancel: cancel, generation: generation, attempt: attempt, cursor: cursor}
		active[item.Key()] = handle
		streams.Add(1)
		go func() {
			defer streams.Done()
			started := time.Now()
			_ = config.Stream(streamCtx, item, handle.cursor, events)
			completion := managedStreamDone{key: item.Key(), generation: handle.generation, attempt: handle.attempt, lifetime: time.Since(started)}
			select {
			case done <- completion:
			case <-ctx.Done():
			}
		}()
	}

	reconcile := func(items []InventoryItem) {
		current := make(map[string]InventoryItem, len(items))
		for _, item := range items {
			key := item.Key()
			current[key] = item
			if previous := active[key]; previous == nil {
				start(item, 0)
			} else if item.Replaced(previous.item) {
				if previous.cancel != nil {
					previous.cancel()
				}
				// A fresh cursor prevents an earlier container's timestamps from
				// suppressing startup logs in a new container incarnation.
				start(item, 0)
			} else {
				previous.item = item
			}
		}
		wanted = current
		for key, handle := range active {
			if _, ok := current[key]; ok {
				continue
			}
			if handle.cancel != nil {
				handle.cancel()
			}
			delete(active, key)
		}
		if config.Updated != nil {
			config.Updated(items, nil)
		}
	}

	stopAll := func() {
		for _, handle := range active {
			if handle.cancel != nil {
				handle.cancel()
			}
		}
		streams.Wait()
	}

	reconcile(config.Items)
	var inventoryTick <-chan time.Time
	var ticker *time.Ticker
	if config.Inventory != nil {
		interval := config.RefreshInterval
		if interval <= 0 {
			interval = 2 * time.Second
		}
		ticker = time.NewTicker(interval)
		inventoryTick = ticker.C
		defer ticker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			stopAll()
			return
		case completion := <-done:
			handle, ok := active[completion.key]
			if !ok || handle.generation != completion.generation {
				continue
			}
			if _, ok := wanted[completion.key]; !ok {
				delete(active, completion.key)
				continue
			}
			nextAttempt := completion.attempt + 1
			if completion.lifetime >= stableStreamTime {
				nextAttempt = 1
			}
			handle.cancel()
			handle.cancel = nil
			handle.attempt = nextAttempt
			// Completed Job/CronJob pods retain logs, but kubectl -f
			// naturally reaches EOF. Do not repeatedly reopen the pod.
			if handle.item.Completed {
				continue
			}
			delay := streamRetryDelay(nextAttempt)
			go func(request managedStreamRetry) {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
					select {
					case retries <- request:
					case <-ctx.Done():
					}
				case <-ctx.Done():
				}
			}(managedStreamRetry{key: completion.key, generation: completion.generation})
		case retry := <-retries:
			handle, ok := active[retry.key]
			if !ok || handle.generation != retry.generation || handle.cancel != nil {
				continue
			}
			item, ok := wanted[retry.key]
			if !ok {
				delete(active, retry.key)
				continue
			}
			// A running pod can become Succeeded while a retry is pending.
			if item.Completed {
				continue
			}
			start(item, handle.attempt)
		case <-inventoryTick:
			limited, cancel := context.WithTimeout(ctx, 10*time.Second)
			items, err := config.Inventory(limited)
			cancel()
			if err == nil {
				reconcile(items)
			} else if config.Updated != nil {
				config.Updated(nil, err)
			}
		}
	}
}
