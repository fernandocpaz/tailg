package kube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fernandocpaz/tailg/internal/core"
)

// PodErrorLogs reads the named pod's complete retained container logs. The
// live buffer, display exclusions, --since, and --tail do not limit this view.
func (r Runner) PodErrorLogs(ctx context.Context, pod string) ([][]core.LogEvent, error) {
	if strings.TrimSpace(pod) == "" {
		return nil, errors.New("no pod selected")
	}
	payload, err := r.JSON(ctx, "get", "pod/"+pod)
	if err != nil {
		return nil, err
	}
	var blocks [][]core.LogEvent
	var failures []error
	seen := make(map[string]bool)
	spec := mapValue(payload["spec"])
	for _, kind := range []string{"containers", "initContainers", "ephemeralContainers"} {
		for _, raw := range sliceValue(spec[kind]) {
			container := stringValue(mapValue(raw)["name"])
			if container == "" || seen[container] {
				continue
			}
			seen[container] = true
			if ctx.Err() != nil {
				failures = append(failures, ctx.Err())
				break
			}
			retained, err := r.containerErrorLogs(ctx, core.InventoryItem{Pod: pod, Container: container})
			blocks = append(blocks, retained...)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s/%s: %w", pod, container, err))
				continue
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("pod/%s has no containers", pod)
	}
	// Sort entries by their root timestamp; stack frames stay with their error.
	sort.SliceStable(blocks, func(i, j int) bool {
		left, right := blocks[i][0].ObservedAt, blocks[j][0].ObservedAt
		if left.IsZero() != right.IsZero() {
			return !left.IsZero()
		}
		return left.Before(right)
	})
	if len(failures) > 0 {
		return blocks, fmt.Errorf("retained error logs incomplete: %w", errors.Join(failures...))
	}
	return blocks, nil
}

// Read without following, retaining only errors so high-volume INFO traffic
// cannot fill the live buffer or be duplicated in memory by this fetch.
func (r Runner) containerErrorLogs(ctx context.Context, item core.InventoryItem) ([][]core.LogEvent, error) {
	events := make(chan core.LogEvent, 64)
	result := make(chan error, 1)
	go func() {
		result <- r.Stream(ctx, item, LogOptions{Tail: -1}, events)
		close(events)
	}()
	var collector core.ErrorLogCollector
	for event := range events {
		collector.Observe(event)
	}
	return collector.Blocks(), <-result
}
