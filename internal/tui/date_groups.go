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
// renderLogTimelineRows positions the viewport without formatting off-screen
// records. This keeps redraw costs bounded even with a large history result.
func renderLogTimelineRows(records []core.LogRecord, query string, selected, height, width int, showPod, color, followsLive bool, now time.Time) []string {
	if height <= 0 || len(records) == 0 {
		return nil
	}

	selectedRecord := selected
	if selectedRecord < 0 || selectedRecord >= len(records) {
		selectedRecord = len(records) - 1
	}
	contextRecord := max(0, selectedRecord-core.SearchContextLines)

	// First pass: count date separators and find the selected row's position.
	// Avoid formatting/colorizing any text outside the eventual viewport.
	location := now.Location()
	lastDay := 0
	total, contextPosition, selectedPosition := 0, 0, 0
	contextStartsGroup := false
	for index, record := range records {
		day := logTimelineDayKey(record.Event.ObservedAt, location)
		startsGroup := day != 0 && day != lastDay
		if startsGroup {
			total++
			lastDay = day
		}
		if index == contextRecord {
			contextPosition = total
			contextStartsGroup = startsGroup
		}
		if index == selectedRecord {
			selectedPosition = total
		}
		total++
	}

	start := 0
	if total > height {
		if followsLive {
			start = total - height
		} else {
			start = contextPosition
			if contextStartsGroup && start > 0 {
				start--
			}
			if selectedPosition >= start+height {
				start = selectedPosition - height + 1
			}
		}
	}
	end := min(total, start+height)

	// Second pass: render only the screen-sized slice, including its date
	// separators. The rest of the history is never formatted during a redraw.
	visible := make([]logTimelineRow, 0, height)
	lastDay = 0
	position := 0
	for index, record := range records {
		day := logTimelineDayKey(record.Event.ObservedAt, location)
		if day != 0 && day != lastDay {
			if position >= start && position < end {
				visible = append(visible, logTimelineRow{
					groupLabel: logDateGroupLabel(record.Event.ObservedAt, now),
					separator: true, recordIndex: -1,
				})
			}
			position++
			lastDay = day
		}
		if position >= start && position < end {
			visible = append(visible, logTimelineRow{
				text: renderRecordRowAt(record, query, index == selected, width, showPod, color, now),
				recordIndex: index,
				groupLabel: logDateGroupLabel(record.Event.ObservedAt, now),
			})
		}
		position++
		if position >= end {
			break
		}
	}

	// Retain the date above the currently visible records without sacrificing
	// the selected/newest row when the terminal height is small.
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

// Integer calendar keys are much cheaper than formatting a date string for
// each record on every redraw. Zero means the transport timestamp is absent.
func logTimelineDayKey(at time.Time, location *time.Location) int {
	if at.IsZero() {
		return 0
	}
	year, month, day := at.In(location).Date()
	return year*10000 + int(month)*100 + day
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

// Use the same local calendar boundary as the date separators. Unknown and
// future timestamps are not evidence of an older log.
func logFromEarlierDay(at, now time.Time) bool {
	return !at.IsZero() && calendarDate(at, now.Location()).Before(calendarDate(now, now.Location()))
}

func fadeHistoricalLog(text string, at, now time.Time, color bool) string {
	if color && logFromEarlierDay(at, now) {
		return lipgloss.NewStyle().Faint(true).Render(text)
	}
	return text
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
