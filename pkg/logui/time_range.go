package logui

import "github.com/fernandocpaz/tailg/internal/tui"

// TimeRange describes a rolling or absolute time filter.
type TimeRange = tui.TimeRange

// TimeResult includes an explicitly incomplete snapshot when appropriate.
type TimeResult struct {
 Events []Event
 Warning string
}
