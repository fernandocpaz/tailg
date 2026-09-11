package kube

import (
	"bufio"
	"context"
	"errors"
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
	Cursor *core.LogCursor
}

func (r Runner) Stream(ctx context.Context, item core.InventoryItem, options LogOptions, output chan<- core.LogEvent) (streamErr error) {
	defer func() {
		if ctx.Err() == nil {
			sendLogEvent(ctx, output, core.LogEvent{Pod: item.Pod, Container: item.Container, Closed: true, Err: streamErr})
		}
	}()
	replay := options.Cursor.Resume()
	args := streamArgs(item, options, replay.Since)
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
	sendLogEvent(ctx, output, core.LogEvent{Pod: item.Pod, Container: item.Container, Started: true})

	scanner := bufio.NewScanner(stdout)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for scanner.Scan() {
		message, observed := SplitTimestamp(scanner.Text())
		event := core.LogEvent{Pod: item.Pod, Container: item.Container, Message: message, ObservedAt: observed, ReceivedAt: time.Now()}
		event.Replayed = replay.Duplicate(event)
		if !sendLogEvent(ctx, output, event) {
			break
		}
		if !event.Replayed {
			options.Cursor.Observe(event)
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

func streamArgs(item core.InventoryItem, options LogOptions, sinceTime time.Time) []string {
	tail := options.Tail
	if !sinceTime.IsZero() {
		// A fixed tail cap on reconnect could skip logs written while disconnected.
		tail = -1
	}
	args := []string{"logs", "pod/" + item.Pod, "-c", item.Container, "--ignore-errors=true", "--timestamps=true", "--tail", strconv.Itoa(tail)}
	if !sinceTime.IsZero() {
		args = append(args, "--since-time", sinceTime.UTC().Format(time.RFC3339))
	} else if options.Since != "" {
		args = append(args, "--since", options.Since)
	}
	if options.Follow {
		args = append(args, "-f")
	}
	return args
}

func sendLogEvent(ctx context.Context, output chan<- core.LogEvent, event core.LogEvent) bool {
	if ctx.Err() != nil {
		return false
	}
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

// CompleteHistory is retained for callers that only render text. Structured
// collection is done by CompleteRecords so a history result can preserve the
// original event and parsed fields all the way to the UI.
func (r Runner) CompleteHistory(ctx context.Context, items []core.InventoryItem, since string, formatter core.Formatter, query string, maxLines int) ([]string, error) {
	records, err := r.CompleteRecords(ctx, items, since, formatter, query, maxLines)
	if err != nil {
		return nil, err
	}
	lines := make([]string, len(records))
	for index, record := range records {
		lines[index] = record.Text
	}
	return lines, nil
}

// CompleteRecords searches complete Kubernetes history for a structured
// query. Every selected stream is collected before the query window is
// applied, so a capped result cannot prevent a later pod from being searched.
func (r Runner) CompleteRecords(ctx context.Context, items []core.InventoryItem, since string, formatter core.Formatter, query string, maxLines int) ([]core.LogRecord, error) {
	if _, err := core.CompileLogQuery(query); err != nil {
		return nil, err
	}
	records, streamErrs, success := r.collectRecords(ctx, items, since, formatter)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if success == 0 && len(streamErrs) > 0 {
		return nil, streamErrs[0]
	}
	return core.SearchRecordsFromFirstMatch(records, query, core.SearchContextLines, maxLines)
}

// CompleteTrace returns all formatted rows carrying traceID, ordered by their
// Kubernetes timestamps across the selected pods. A positive maxLines bounds
// the chronologically ordered result; a negative value leaves it unbounded.
// Collection errors are returned alongside partial records so the caller can
// display useful results while making an incomplete trace explicit.
func (r Runner) CompleteTrace(ctx context.Context, items []core.InventoryItem, since string, formatter core.Formatter, traceID string, maxLines int) ([]core.LogRecord, error) {
	rawTraceID := traceID
	traceID = normalizeTraceID(traceID)
	if traceID == "" {
		return nil, fmt.Errorf("invalid trace ID %q: expected 32 hexadecimal characters", rawTraceID)
	}
	records, streamErrs, success := r.collectRecords(ctx, items, since, formatter)
	if ctx.Err() != nil && len(streamErrs) == 0 {
		streamErrs = []error{ctx.Err()}
	}
	filtered := records[:0]
	for _, record := range records {
		if record.Fields.TraceID == traceID {
			filtered = append(filtered, record)
		}
	}
	var truncationErr error
	if maxLines >= 0 && len(filtered) > maxLines {
		truncationErr = fmt.Errorf("trace history truncated: showing %d of %d matching records (maxLines %d)", maxLines, len(filtered), maxLines)
		filtered = filtered[:maxLines]
	}
	if len(streamErrs) == 0 {
		return filtered, truncationErr
	}
	joined := errors.Join(streamErrs...)
	if success == 0 {
		return nil, fmt.Errorf("trace history collection failed: %w", joined)
	}
	if truncationErr != nil {
		joined = errors.Join(joined, truncationErr)
	}
	return filtered, fmt.Errorf("trace history incomplete: %d of %d streams failed: %w", len(items)-success, len(items), joined)
}

// collectRecords deliberately has no output cap. It is shared by normal
// history search and trace lookup, both of which must inspect every selected
// stream before limiting results.
func (r Runner) collectRecords(ctx context.Context, items []core.InventoryItem, since string, formatter core.Formatter) ([]core.LogRecord, []error, int) {
	var records []core.LogRecord
	var streamErrs []error
	success := 0
	var nextID uint64 = 1
	for _, item := range items {
		if ctx.Err() != nil {
			streamErrs = append(streamErrs, ctx.Err())
			break
		}
		events, err := r.Snapshot(ctx, item, LogOptions{Since: since, Tail: -1})
		if err != nil {
			if ctx.Err() != nil {
				streamErrs = append(streamErrs, ctx.Err())
				break
			}
			streamErrs = append(streamErrs, fmt.Errorf("%s/%s: %w", item.Pod, item.Container, err))
			continue
		}
		success++
		for _, event := range events {
			for _, record := range core.RecordsForEvent(formatter, event, true) {
				record.ID = nextID
				nextID++
				records = append(records, record)
			}
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i].Event.ObservedAt, records[j].Event.ObservedAt
		if left.IsZero() != right.IsZero() {
			return !left.IsZero()
		}
		if !left.Equal(right) {
			return left.Before(right)
		}
		return records[i].ID < records[j].ID
	})
	return records, streamErrs, success
}

func normalizeTraceID(value string) string {
	text := strings.TrimSpace(value)
	if len(text) != 32 || !isHexTraceID(text) || strings.Trim(text, "0") == "" {
		return ""
	}
	return strings.ToLower(text)
}

func isHexTraceID(value string) bool {
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') && !(char >= 'A' && char <= 'F') {
			return false
		}
	}
	return true
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
