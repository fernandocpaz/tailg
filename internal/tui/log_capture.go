package tui

import (
	"io"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

type logCapture struct {
	output  io.Writer
	lastDay int
}

func (c *logCapture) write(records []core.LogRecord, now time.Time) error {
	var batch strings.Builder
	for _, record := range records {
		if day := logTimelineDayKey(record.Event.ObservedAt, now.Location()); day != 0 && day != c.lastDay {
			batch.WriteString("── ")
			batch.WriteString(logDateGroupLabel(record.Event.ObservedAt, now))
			batch.WriteByte('\n')
			c.lastDay = day
		}
		batch.WriteString(record.Text)
		if core.IsSlowRequest(record.Fields) {
			batch.WriteString(" SLOW")
		}
		batch.WriteByte('\n')
	}
	_, err := io.WriteString(c.output, batch.String())
	return err
}
