package core

import "regexp"

var errorLogToken = regexp.MustCompile(`(?i)\b(?:ERR|FTL)\b`)

// ErrorLogCollector keeps only matching entries and exception continuations
// while complete retained stdout is read. Ordinary traffic is not buffered.
type ErrorLogCollector struct {
	blocks [][]LogEvent
	active map[string]int
}

func (c *ErrorLogCollector) Observe(event LogEvent) {
	if event.Started || event.Closed {
		return
	}
	if c.active == nil {
		c.active = make(map[string]int)
	}
	key := event.Pod + "\x00" + event.Container
	if index, ok := c.active[key]; ok && IsLogContinuation(event.Message) {
		c.blocks[index] = append(c.blocks[index], event)
		return
	}
	delete(c.active, key)
	level := ParseLogFields(event).Level
	if level == "ERR" || (level == "" && errorLogToken.MatchString(StripANSI(event.Message))) {
		c.active[key] = len(c.blocks)
		c.blocks = append(c.blocks, []LogEvent{event})
	}
}

func (c *ErrorLogCollector) Blocks() [][]LogEvent { return c.blocks }

// ErrorLogBlocks keeps each matching entry and its exception continuations,
// without collapsing repeated errors or borrowing lines from another stream.
func ErrorLogBlocks(events []LogEvent) [][]LogEvent {
	var collector ErrorLogCollector
	for _, event := range events {
		collector.Observe(event)
	}
	return collector.Blocks()
}
