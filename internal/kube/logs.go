package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

type LogOptions struct {
	Since  string
	Tail   int
	Follow bool
}

func (r Runner) Stream(ctx context.Context, item core.InventoryItem, options LogOptions, output chan<- core.LogEvent) (streamErr error) {
	defer func() {
		if ctx.Err() == nil {
			sendLogEvent(ctx, output, core.LogEvent{Pod: item.Pod, Container: item.Container, Closed: true, Err: streamErr})
		}
	}()
	args := []string{"logs", "pod/" + item.Pod, "-c", item.Container, "--ignore-errors=true", "--timestamps=true", "--tail", strconv.Itoa(options.Tail)}
	if options.Since != "" {
		args = append(args, "--since", options.Since)
	}
	if options.Follow {
		args = append(args, "-f")
	}
	cmd := r.Command(ctx, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	stderrDone := make(chan []byte, 1)
	go func() {
		stderrBytes, _ := io.ReadAll(stderr)
		stderrDone <- stderrBytes
	}()

	scanner := bufio.NewScanner(stdout)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for scanner.Scan() {
		message, observed := SplitTimestamp(scanner.Text())
		if !sendLogEvent(ctx, output, core.LogEvent{Pod: item.Pod, Container: item.Container, Message: message, ObservedAt: observed}) {
			break
		}
	}
	scanErr := scanner.Err()
	stderrBytes := <-stderrDone
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		message := strings.TrimSpace(string(stderrBytes))
		if message == "" {
			return fmt.Errorf("kubectl logs %s/%s: %w", item.Pod, item.Container, waitErr)
		}
		return fmt.Errorf("kubectl logs %s/%s: %s", item.Pod, item.Container, message)
	}
	return nil
}

func sendLogEvent(ctx context.Context, output chan<- core.LogEvent, event core.LogEvent) bool {
	select {
	case output <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r Runner) Snapshot(ctx context.Context, item core.InventoryItem, options LogOptions) ([]core.LogEvent, error) {
	args := []string{"logs", "pod/" + item.Pod, "-c", item.Container, "--ignore-errors=true", "--timestamps=true", "--tail", strconv.Itoa(options.Tail)}
	if options.Since != "" {
		args = append(args, "--since", options.Since)
	}
	text, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var events []core.LogEvent
	scanner := bufio.NewScanner(strings.NewReader(text))
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for scanner.Scan() {
		message, observed := SplitTimestamp(scanner.Text())
		events = append(events, core.LogEvent{Pod: item.Pod, Container: item.Container, Message: message, ObservedAt: observed})
	}
	return events, scanner.Err()
}

func (r Runner) CompleteHistory(ctx context.Context, items []core.InventoryItem, since string, formatter core.Formatter, query string, maxLines int) ([]string, error) {
	type row struct {
		at       time.Time
		sequence int
		text     string
	}
	var rows []row
	sequence := 0
	success := 0
	var firstErr error
	for _, item := range items {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		events, err := r.Snapshot(ctx, item, LogOptions{Since: since, Tail: -1})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		success++
		for _, event := range events {
			for _, line := range formatter.Format(item.Pod, item.Container, event.Message, true) {
				rows = append(rows, row{event.ObservedAt, sequence, line})
				sequence++
			}
		}
	}
	if success == 0 && firstErr != nil {
		return nil, firstErr
	}
	if len(items) > 1 {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].at.IsZero() != rows[j].at.IsZero() {
				return !rows[i].at.IsZero()
			}
			if rows[i].at.Equal(rows[j].at) {
				return rows[i].sequence < rows[j].sequence
			}
			return rows[i].at.Before(rows[j].at)
		})
	}
	lines := make([]string, len(rows))
	for i, row := range rows {
		lines[i] = row.text
	}
	return core.SearchLinesFromFirstMatch(lines, query, core.SearchContextLines, maxLines), nil
}

func SplitTimestamp(line string) (string, time.Time) {
	first, rest, ok := strings.Cut(line, " ")
	if !ok {
		return line, time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, first)
	if err != nil {
		return line, time.Time{}
	}
	return rest, parsed.Local()
}
