package core

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLogBlockIncludesEntireSQLErrorFromAnySelectedLine(t *testing.T) {
	messages := []string{
		"[10:06:53 INF] previous request",
		"[10:06:54 ERR] DB query ExecuteReaderAsync | SqlPreview=SELECT ...",
		"Microsoft.Data.SqlClient.SqlException (0x80131904): The request failed.",
		" ---> System.InvalidOperationException: inner failure",
		"   at System.Threading.ExecutionContext.RunInternal()",
		"--- End of stack trace from previous location ---",
		"   at Vitas.Emr.Api.Patient.Infrastructure.Diagnostics.TracingDbCommand.ExecuteWithTracingAsync()",
		"ClientConnectionId:fd813b5b-0323-48b8-9251-6fcc24189628",
		"Error Number:3980,State:1,Class:16",
		"  [10:06:55 INF] next request",
	}
	var events []LogEvent
	for i, message := range messages {
		event := LogEvent{Pod: "patient-1", Container: "api", Message: message, ObservedAt: time.Unix(int64(i), 0)}
		events = append(events, event)
		other := event
		other.Pod, other.Message = "patient-2", "[10:06:54 ERR] unrelated pod"
		events = append(events, other)
		other.Pod, other.Container = event.Pod, "sidecar"
		events = append(events, other)
	}
	for i := 1; i < len(messages)-1; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			block, ok := LogBlock(events, events[i*3])
			if !ok || len(block) != len(messages)-2 {
				t.Fatalf("block has %d lines, found=%t; want %d", len(block), ok, len(messages)-2)
			}
			for j, event := range block {
				if event.Message != messages[j+1] || event.Pod != "patient-1" || event.Container != "api" {
					t.Fatalf("unexpected block line %d: %+v", j, event)
				}
			}
		})
	}
}

func TestLogBlockNoLineCapAndRespectsEntryBoundaries(t *testing.T) {
	root := LogEvent{Pod: "p", Container: "c", Message: "[10:00:00 ERR] failed"}
	events := []LogEvent{root}
	for i := 0; i < 2000; i++ {
		events = append(events, LogEvent{Pod: "p", Container: "c", Message: fmt.Sprintf("   at Frame%d()", i)})
	}
	for _, boundary := range []string{
		`{"level":"INFO","message":"next"}`, "[10:00:01 ERR] next", "2026-09-28T10:00:01Z next", "unrelated plain output",
	} {
		all := append(append([]LogEvent(nil), events...), LogEvent{Pod: "p", Container: "c", Message: boundary})
		block, ok := LogBlock(all, root)
		if !ok || len(block) != 2001 || block[len(block)-1].Message != "   at Frame1999()" {
			t.Fatalf("boundary %q: got %d lines, found=%t", boundary, len(block), ok)
		}
	}
	missing := root
	missing.Message = "[10:00:00 ERR] rotated"
	if _, found := LogBlock(events, missing); found {
		t.Fatal("a different error was substituted for a missing anchor")
	}
}

func TestSelectedBlockKeepsContinuationsHiddenByFilter(t *testing.T) {
	state := NewFilterState(20)
	formatter := Formatter{}
	var records []LogRecord
	for _, message := range []string{"[10:00:00 ERR] failure", "   at Inner()", "Error Number:3980,State:1,Class:16", "[10:00:01 INF] next"} {
		records = append(records, RecordsForEvent(formatter, LogEvent{Pod: "p", Container: "c", Message: message}, false)...)
	}
	for _, history := range []bool{false, true} {
		state.SetFilter("level:error")
		state.SetMatchesOnly(true)
		if history {
			state.SetSearchRecords("level:error", records)
		} else {
			state.AppendRecords(records...)
		}
		selected, ok := state.SelectedRecord(0)
		if !ok {
			t.Fatal("error missing from filtered view")
		}
		block := state.SelectedBlock(selected)
		if len(block) != 3 || !strings.Contains(block[2].Message, "3980") {
			t.Fatalf("history=%t: block missing hidden stack lines: %+v", history, block)
		}
	}
}
