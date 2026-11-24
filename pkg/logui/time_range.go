package logui

import "github.com/fernandocpaz/tailg/internal/tui"

// TimeRange describes a rolling or absolute time filter.
type TimeRange = tui.TimeRange

// TimeResult includes an explicitly incomplete snapshot when appropriate.
type TimeResult struct {
 Events []Event
 Warning string
}

// ReadSharedTimeRange returns the saved selection without any log data.
func ReadSharedTimeRange(path string)(TimeRange,bool){return tui.ReadSharedTimeRange(path)}
