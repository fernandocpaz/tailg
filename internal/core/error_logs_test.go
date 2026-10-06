package core

import (
	"strings"
	"testing"
)

func TestErrorLogBlocksKeepsFatalErrorsStacksAndRepeatedEntries(t *testing.T) {
	var events []LogEvent
	add := func(pod, container, message string) {
		events = append(events, LogEvent{Pod: pod, Container: container, Message: message})
	}
	add("p", "api", "[10:00:00 INF] ordinary request")
	add("p", "api", "[10:00:01 ERR] database failed")
	add("p", "api", "   at FrameOne()")
	add("p", "sidecar", "   at UnrelatedFrame()")
	add("p", "api", "Error Number:3980,State:1,Class:16")
	add("p", "api", "   [10:00:02 FTL] fatal failure")
	add("p", "api", "   at FatalFrame()")
	add("p", "api", `{"@l":"Fatal","@m":"fatal JSON","@x":"full exception","Properties":{"secretContext":"kept"}}`)
	add("p", "api", "[10:00:03 INF] normal again")
	add("p", "api", "   at NotAnError()")
	add("p", "api", "[10:00:01 ERR] database failed")
	add("p", "api", "ERR plain failure")
	add("p", "api", "FTL plain fatal")
	add("p", "api", "unstructured ERR failure")
	blocks := ErrorLogBlocks(events)
	if len(blocks) != 7 || len(blocks[0]) != 3 || len(blocks[1]) != 2 {
		t.Fatalf("wrong error or continuation count: %+v", blocks)
	}
	for _, block := range blocks {
		for _, event := range block {
			if strings.Contains(event.Message, "ordinary") || strings.Contains(event.Message, "Unrelated") || strings.Contains(event.Message, "NotAnError") {
				t.Fatalf("non-error or another stream leaked into error history: %+v", event)
			}
		}
	}
	if blocks[2][0].Message != events[7].Message || blocks[3][0].Message != blocks[0][0].Message {
		t.Fatal("raw JSON or repeated error was lost")
	}
}

func TestFatalAliasesParseFormatAndStartNewBlocks(t *testing.T) {
	for _, message := range []string{"[10:00:00 FTL] fatal", "FTL: fatal", "[FATAL] fatal", "CRITICAL fatal", `{"level":"FTL","message":"fatal"}`} {
		event := LogEvent{Message: message}
		if level := ParseLogFields(event).Level; level != "ERR" {
			t.Fatalf("%s parsed as %s", message, level)
		}
		if IsLogContinuation("  " + message) {
			t.Fatalf("fatal entry mistaken for a preceding stack frame: %s", message)
		}
		if rows := (Formatter{Color: true}).Format("p", "c", message, false); len(rows) != 1 || !strings.Contains(rows[0], colors["red"]) {
			t.Fatalf("fatal entry not rendered red: %q", rows)
		}
	}
}
