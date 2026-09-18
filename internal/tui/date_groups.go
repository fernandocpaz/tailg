package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
)

type logTimelineRow struct {
	text        string
	recordIndex int
	groupLabel  string
	separator   bool
}

// renderLogTimelineRows adds non-selectable date separators while keeping the
// viewport anchored to the newest log in live mode and the selected log while
// paused or searching.
func renderLogTimelineRows(records []core.LogRecord, query string, selected, height, width int, showPod, color, followsLive bool, now time.Time) []string {
	if height <= 0 || len(records) == 0 {
		return nil
	}

	timeline := make([]logTimelineRow, 0, len(records)+1)
	recordPositions := make([]int, len(records))
	lastDate := ""
	for index, record := range records {
		groupLabel := logDateGroupLabel(record.Event.ObservedAt, now)
		if groupLabel != "" {
			date := localDateKey(record.Event.ObservedAt, now.Location())
			if date != lastDate {
				timeline = append(timeline, logTimelineRow{
					groupLabel:  groupLabel,
					separator:   true,
					recordIndex: -1,
				})
				lastDate = date
			}
		}
		recordPositions[index] = len(timeline)
		timeline = append(timeline, logTimelineRow{
			text:        renderRecordRow(record, query, index == selected, width, showPod, color),
			recordIndex: index,
			groupLabel:  groupLabel,
		})
	}

	selectedRecord := selected
	if selectedRecord < 0 || selectedRecord >= len(records) {
		selectedRecord = len(records) - 1
	}

	start := 0
	if len(timeline) > height {
		if followsLive {
			start = len(timeline) - height
		} else {
			contextRecord := max(0, selectedRecord-core.SearchContextLines)
			start = recordPositions[contextRecord]
			if start > 0 && timeline[start-1].separator && timeline[start-1].groupLabel == timeline[start].groupLabel {
				start--
			}
			selectedPosition := recordPositions[selectedRecord]
			if selectedPosition >= start+height {
				start = selectedPosition - height + 1
			}
		}
	}
	end := min(len(timeline), start+height)
	visible := append([]logTimelineRow(nil), timeline[start:end]...)

	// When the viewport starts inside a date group, keep its date visible as a
	// sticky first row. If space is tight, discard an older unselected log—not
	// the selected or newest record.
	if height > 1 && start > 0 && len(visible) > 0 && !visible[0].separator && visible[0].groupLabel != "" {
		sticky := logTimelineRow{groupLabel: visible[0].groupLabel, separator: true, recordIndex: -1}
		visible = append([]logTimelineRow{sticky}, visible...)
		if len(visible) > height {
			drop := 1
			if visible[drop].recordIndex == selectedRecord {
				drop = len(visible) - 1
			}
			visible = append(visible[:drop], visible[drop+1:]...)
		}
	}

	lines := make([]string, 0, len(visible))
	for _, row := range visible {
		if row.separator {
			lines = append(lines, renderLogDateGroupRow(row.groupLabel, width, color))
		} else {
			lines = append(lines, row.text)
		}
	}
	return lines
}

func logDateGroupLabel(observedAt, now time.Time) string {
	if observedAt.IsZero() {
		return ""
	}
	location := now.Location()
	observed := observedAt.In(location)
	exact := observed.Format("2006-01-02")
	today := calendarDate(now, location)
	date := calendarDate(observedAt, location)
	days := int(today.Sub(date) / (24 * time.Hour))

	switch {
	case days == 0:
		return "Today · " + exact
	case days == 1:
		return "1 day ago · " + exact
	case days > 1:
		return fmt.Sprintf("%d days ago · %s", days, exact)
	case days == -1:
		return "1 day ahead · " + exact
	default:
		return fmt.Sprintf("%d days ahead · %s", -days, exact)
	}
}

func localDateKey(value time.Time, location *time.Location) string {
	if value.IsZero() {
		return ""
	}
	return value.In(location).Format("2006-01-02")
}

// Constructing UTC midnights from local calendar fields makes the day count
// immune to 23- and 25-hour daylight-saving days.
func calendarDate(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

func renderLogDateGroupRow(label string, width int, color bool) string {
	if label == "" {
		return ""
	}
	prefix := "── "
	labelWidth := lipgloss.Width(label)
	if width <= lipgloss.Width(prefix)+labelWidth {
		// Keep both the relative and exact date even in an unusually narrow
		// terminal; losing the date would be more harmful than a soft overflow.
		return renderWithColor(headerStyle, label, color)
	}
	suffix := " " + strings.Repeat("─", max(0, width-lipgloss.Width(prefix)-labelWidth-1))
	return renderWithColor(dimStyle, prefix, color) +
		renderWithColor(headerStyle, label, color) +
		renderWithColor(dimStyle, suffix, color)
}
