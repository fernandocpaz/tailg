package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

type captureRecord struct {
	SchemaVersion string   `json:"schemaVersion"`
	Timestamp     string   `json:"timestamp,omitempty"`
	Context       string   `json:"context,omitempty"`
	Namespace     string   `json:"namespace"`
	Pod           string   `json:"pod"`
	Container     string   `json:"container"`
	Level         string   `json:"level,omitempty"`
	Service       string   `json:"service,omitempty"`
	TraceID       string   `json:"traceId,omitempty"`
	SpanID        string   `json:"spanId,omitempty"`
	RequestID     string   `json:"requestId,omitempty"`
	Method        string   `json:"method,omitempty"`
	Path          string   `json:"path,omitempty"`
	StatusCode    int      `json:"statusCode,omitempty"`
	DurationMs    *float64 `json:"durationMs,omitempty"`
	Slow          bool     `json:"slow,omitempty"`
	Message       string   `json:"message"`
	Raw           string   `json:"raw,omitempty"`
}

type LogCapture struct {
	output             io.Writer
	context, namespace string
}

func NewLogCapture(output io.Writer, kubeContext, namespace string) LogCapture {
	return LogCapture{output: output, context: kubeContext, namespace: namespace}
}

// WriteBatch writes one JSON object per original log event. Each event can have
// multiple formatted rows, for example a message and its exception stack.
func (c LogCapture) WriteBatch(events [][]core.LogRecord) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	for _, rows := range events {
		if len(rows) == 0 {
			continue
		}
		first := rows[0]
		event, fields := first.Event, first.Fields
		var display strings.Builder
		for index, row := range rows {
			if index > 0 {
				display.WriteByte('\n')
			}
			display.WriteString(ansi.Strip(row.Text))
		}
		record := captureRecord{
			SchemaVersion: "tailg.log/v1", Timestamp: timestamp(event.ObservedAt),
			Context: c.context, Namespace: c.namespace, Pod: event.Pod, Container: event.Container,
			Level: fields.Level, Service: Redact(fields.Service), TraceID: fields.TraceID, SpanID: fields.SpanID,
			RequestID: Redact(fields.RequestID), Method: Redact(fields.Method), Path: Redact(fields.Path),
			StatusCode: fields.StatusCode, Slow: core.IsSlowRequest(fields), Message: Redact(display.String()),
		}
		if fields.HasDuration {
			ms := float64(fields.Duration) / float64(time.Millisecond)
			record.DurationMs = &ms
		}
		if raw := Redact(ansi.Strip(event.Message)); raw != record.Message {
			record.Raw = raw
		}
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("encode log record: %w", err)
		}
	}
	if _, err := buffer.WriteTo(c.output); err != nil {
		return fmt.Errorf("write log file: %w", err)
	}
	return nil
}
