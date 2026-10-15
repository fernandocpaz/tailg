package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

func (m pickerModel) pickerScreen() string {
	width := m.width
	if width <= 0 {
		width = 160
	}
	if m.height > 0 && (m.height < 10 || width < 24) {
		return pickerScreenFrame([]string{headerStyle.Render("tailg"), "Resize terminal to browse workloads."},
			[]string{"? Help · Q Quit"}, width, m.height)
	}
	header := m.pickerScreenHeader(width)
	footer := m.pickerScreenFooter(width)
	if m.loadError != "" {
		label := "Inventory warning: "
		if len(m.apps) == 0 {
			label = "Could not load applications: "
		}
		header = append(header, pickerLimitedLines(label+m.loadError, width, 2)...)
	}
	if m.note != "" {
		header = append(header, pickerLimitedLines(m.note, width, 2)...)
	}
	if m.totalApps > len(m.apps) {
		jobs := 0
		for _, app := range m.apps {
			if app.CronJob != nil {
				jobs++
			}
		}
		header = append(header, pickerLimitedLines(fmt.Sprintf("Showing newest %d of %d applications and all %d CronJobs", len(m.apps)-jobs, m.totalApps-jobs, jobs), width, 1)...)
	}
	if len(m.apps) == 0 {
		if m.loadError == "" {
			header = append(header, pickerWrapLines([]string{"No applications found in this namespace."}, width)...)
		}
		header = append(header, pickerWrapLines([]string{"Press C to switch context, N to choose namespace, or R to retry."}, width)...)
		return pickerScreenFrame(header, footer, width, m.height)
	}
	// Always leave room for a section heading, the cursor row and a scroll hint.
	// Compact ancillary content first on short terminals; controls stay reachable.
	if m.height > 0 && len(header)+len(footer)+4 > m.height {
		header = header[:min(len(header), max(1, m.height-len(footer)-4))]
	}
	rows := make([]pickerViewportRow, len(m.apps))
	appColumns, jobColumns := pickerColumns(width, false), pickerColumns(width, true)
	var headers [2][]string
	for section, columns := range [][]pickerColumn{appColumns, jobColumns} {
		headers[section] = []string{dimStyle.Render(pickerTableLine("      ", columns, nil)), dimStyle.Render(strings.Repeat("─", width))}
	}
	now := time.Now()
	for i, app := range m.apps {
		columns := appColumns
		if app.CronJob != nil {
			columns = jobColumns
			rows[i].section = 1
		}
		values := pickerRowValues(app, now)
		marker := "  [ ] "
		if m.checked[app.Name] {
			marker = "  [x] "
		}
		if i == m.index {
			marker = "> " + marker[2:]
		}
		style := lipgloss.NewStyle()
		if i == m.index {
			style = selectedStyle
		}
		line := pickerTableLine(marker, columns, values)
		if m.monitorEnabled {
			nameWidth := columns[0].width
			name := pickerCell(values["name"], nameWidth)
			prefix := style.Render(marker)
			suffix := style.Render(ansi.Cut(line, 6+nameWidth, width))
			line = prefix + renderMonitoredPodName(name, m.monitorApps[app.Name], m.monitorFlashOn) + suffix
		} else {
			line = style.Render(line)
		}
		rows[i].lines = []string{line}
	}
	budget := m.height - len(header) - len(footer)
	start, end := pickerViewportRange(rows, headers, m.index, budget, 1, m.height > 0)
	for i := start; i < end; i++ {
		if i == start || rows[i].section != rows[i-1].section {
			header = append(header, headers[rows[i].section]...)
		}
		header = append(header, rows[i].lines...)
	}
	if start > 0 || end < len(rows) {
		header = append(header, dimStyle.Render(truncatePickerCell(fmt.Sprintf("Rows %d–%d of %d · ↑↓ to scroll", start+1, end, len(rows)), width)))
	}
	return pickerScreenFrame(header, footer, width, m.height)
}

func (m pickerModel) pickerScreenHeader(width int) []string {
	scope := func(binding key.Binding, label, value string, available int) string {
		return headerStyle.Render(binding.Help().Key) + " " + label + ": " + truncatePickerCell(value, max(1, available-lipgloss.Width(label)-4))
	}
	contextWidth := max(12, (width-12)/2)
	namespaceWidth := max(12, width-12-contextWidth)
	context := scope(pickerKeys.context, "Context", m.kubeContext, contextWidth)
	namespace := scope(pickerKeys.namespace, "Namespace", m.namespace, namespaceWidth)
	line := headerStyle.Render("tailg") + "    " + context + "    " + namespace
	var lines []string
	if lipgloss.Width(line) <= width {
		lines = append(lines, line)
	} else {
		lines = append(lines, headerStyle.Render("tailg")+"  "+scope(pickerKeys.context, "Context", m.kubeContext, width-7))
		lines = append(lines, scope(pickerKeys.namespace, "Namespace", m.namespace, width))
	}
	inspect := dimStyle.Render("INSPECT  ") + strings.Join(pickerKeyHints(width, pickerKeys.changes, pickerKeys.events), "  ")
	watch := dimStyle.Render("WATCH  ") + headerStyle.Render("M") + " " + podMonitorSummary(m.monitorEnabled, m.monitorBusy, m.monitorLastScan, m.monitorError)
	if lipgloss.Width(inspect+"    "+watch) <= width {
		lines = append(lines, inspect+"    "+watch)
	} else {
		lines = append(lines, inspect)
		lines = append(lines, pickerLimitedLines(watch, width, 2)...)
	}
	return append(pickerWrapLines(lines, width), dimStyle.Render(strings.Repeat("─", width)))
}

func (m pickerModel) pickerScreenFooter(width int) []string {
	var footer []string
	if m.index >= 0 && m.index < len(m.apps) {
		app := m.apps[m.index]
		values := pickerRowValues(app, time.Now())
		detail := values["name"] + " · Image: " + values["image"] + " · Restarts: " + values["restarts"] + " · Started: " + values["started"] + " · Deployed: " + values["deployed"]
		if app.CronJob != nil {
			last := "never"
			if !app.CronJob.LastScheduledAt.IsZero() {
				last = app.CronJob.LastScheduledAt.Local().Format("2006-01-02 15:04:05 MST")
			}
			detail = fmt.Sprintf("%s · Schedule: %s (%s) · %s · Active: %s · Pods: %s · Last scheduled: %s", values["name"], values["schedule"], values["zone"], values["state"], values["active"], values["pods"], last)
		}
		limit := 2
		if m.height > 0 && m.height < 20 {
			limit = 1
		}
		footer = append(footer, pickerLimitedLines(detail, width, limit)...)
	}
	if len(m.checked) > 0 {
		pods := 0
		for _, app := range m.checkedChoices() {
			pods += len(app.Pods)
		}
		footer = append(footer, pickerLimitedLines(fmt.Sprintf("Selected: %d applications · %d current pods", len(m.checked), pods), width, 1)...)
	}
	footer = append(footer, dimStyle.Render(strings.Repeat("─", width)))
	move := pickerBinding("↑↓", "Move", "up", "down")
	bindings := []key.Binding{move, pickerKeys.mark, pickerKeys.open, pickerKeys.pods, pickerKeys.refresh}
	isCronJob := m.index >= 0 && m.index < len(m.apps) && m.apps[m.index].CronJob != nil
	if isCronJob {
		bindings = append(bindings, pickerKeys.run)
	}
	bindings = append(bindings, pickerKeys.help, pickerKeys.quit)
	if width < 60 || (m.height > 0 && m.height < 16) {
		bindings = []key.Binding{move, pickerKeys.open}
		if isCronJob {
			bindings = append(bindings, pickerKeys.run)
		}
		bindings = append(bindings, pickerKeys.help, pickerKeys.quit)
	}
	return append(footer, pickerKeyHints(width, bindings...)...)
}

func pickerLimitedLines(text string, width, limit int) []string {
	lines := strings.Split(ansi.Wrap(text, width, ""), "\n")
	if len(lines) > limit {
		lines = lines[:limit]
		lines[limit-1] = ansi.Truncate(lines[limit-1], width-1, "") + "…"
	}
	return lines
}

// Compose exactly one terminal viewport; the footer stays at the bottom even
// when the namespace is empty. No trailing newline can scroll the last row away.
func pickerScreenFrame(body, footer []string, width, height int) string {
	if height > 0 {
		footer = footer[max(0, len(footer)-height):]
		body = body[:min(len(body), height-len(footer))]
		for len(body)+len(footer) < height {
			body = append(body, "")
		}
	}
	lines := append(body, footer...)
	for i, line := range lines {
		lines[i] = modalCells(line, 0, width)
	}
	return strings.Join(lines, "\n")
}

type pickerColumn struct {
	id, title string
	width     int
}

func pickerColumns(width int, cronJob bool) []pickerColumn {
	columns := []pickerColumn{{"name", "APPLICATION", 0}}
	add := func(minimum int, id, title string, size int) {
		if width >= minimum {
			columns = append(columns, pickerColumn{id, title, size})
		}
	}
	if cronJob {
		columns[0].title = "CRONJOB"
		schedule := 12
		if width >= 100 {
			schedule = 22
		} else if width >= 75 {
			schedule = 17
		}
		add(38, "schedule", "SCHEDULE", schedule)
		add(150, "zone", "TIME ZONE", 26)
		add(65, "state", "STATE", 10)
		add(110, "active", "ACTIVE", 6)
		add(80, "pods", "PODS", 4)
		add(125, "last", "LAST SCHEDULED", 14)
	} else {
		add(38, "ready", "READY", 5)
		add(38, "phase", "PHASE", 12)
		add(55, "restarts", "RESTARTS", 8)
		add(100, "started", "STARTED", 10)
		add(120, "deployed", "DEPLOYED", 10)
		add(75, "image", "IMAGE", 12)
	}
	remaining := width - 6 - (len(columns) - 1)
	for _, column := range columns[1:] {
		remaining -= column.width
	}
	columns[0].width = max(1, remaining)
	// Share extra space with image tags instead of leaving one enormous name cell.
	if !cronJob && width >= 120 {
		extra := max(0, remaining-pickerAppWidth)
		columns[0].width -= extra
		columns[len(columns)-1].width += extra
	}
	return columns
}

func pickerCell(value string, width int) string {
	value = truncatePickerCell(value, width)
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func pickerTableLine(marker string, columns []pickerColumn, values map[string]string) string {
	cells := make([]string, len(columns))
	for i, column := range columns {
		value := column.title
		if values != nil {
			value = values[column.id]
		}
		cells[i] = pickerCell(value, column.width)
	}
	return marker + strings.Join(cells, " ")
}

func pickerRowValues(app core.AppChoice, now time.Time) map[string]string {
	values := map[string]string{"name": app.Name, "ready": valueOrDash(app.Ready), "phase": valueOrDash(app.Phases),
		"restarts": fmt.Sprint(app.Restarts), "started": formatPickerAge(now, app.StartedAt), "deployed": formatPickerAge(now, app.DeployedAt), "image": valueOrDash(app.ImageTag)}
	if job := app.CronJob; job != nil {
		zone, state, last := job.TimeZone, "Scheduled", "never"
		if zone == "" {
			zone = "controller local"
		}
		if job.Suspended {
			state = "Suspended"
		}
		if !job.LastScheduledAt.IsZero() {
			last = formatPickerAge(now, job.LastScheduledAt)
		}
		values["name"], values["schedule"], values["zone"], values["state"] = job.Name, job.Schedule, zone, state
		values["active"], values["pods"], values["last"] = fmt.Sprint(job.Active), fmt.Sprint(len(app.Pods)), last
	}
	return values
}
