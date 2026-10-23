package tui

import (
	"strings"
	"testing"
)

func TestLevelFirstBracketedTimestampColumns(t *testing.T) {
	tests := []struct {
		input, level, time string
	}{
		{"ERROR [09:21:35.286] [health check] AuthService returned 401", "ERR", "09:21:35.286"},
		{"FATAL [09:21:35.291] [health check] Service halted", "ERR", "09:21:35.291"},
		{"WARNING [09:21:35.300] [health check] Degraded", "WRN", "09:21:35.300"},
		{"INFO [09:21:34.000] [health check] OK", "INF", "09:21:34.000"},
		{"[09:21:35.291 ERR] Existing Serilog message", "ERR", "09:21:35.291"},
	}
	for _, tt := range tests {
		columns := parseLogColumns(tt.input, false)
		if columns.level != tt.level || columns.time != tt.time {
			t.Errorf("parseLogColumns(%q) = %+v, want %s %s", tt.input, columns, tt.level, tt.time)
		}
		if columns.level != "" && strings.HasPrefix(columns.message, tt.level) {
			t.Errorf("level was left in message: %+v", columns)
		}
	}
	columns := parseLogColumns("[pod-7d9] FATAL [09:21:35.291] [health check] Service halted", true)
	if columns.pod != "pod-7d9" || columns.level != "ERR" || columns.time != "09:21:35.291" {
		t.Fatalf("pod-prefixed format failed: %+v", columns)
	}
}
