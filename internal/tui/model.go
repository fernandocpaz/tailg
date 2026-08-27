package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
)

type Config struct {
	Title           string
	Namespace       string
	KubeContext     string
	Target          string
	Items           []core.InventoryItem
	Formatter       core.Formatter
	HeartbeatWindow time.Duration
	RefreshInterval time.Duration
	BufferLines     int
	FilterFile      string
	Stream          func(context.Context, core.InventoryItem, chan<- core.LogEvent) error
	Inventory       func(context.Context) ([]core.InventoryItem, error)
	Search          func(context.Context, string) ([]string, error)
	MappedResources func(context.Context, string) ([]core.MappedResource, error)
	ResourceDetail  func(context.Context, core.MappedResource) (string, error)
}

type logMsg core.LogEvent
type inventoryMsg struct {
	items []core.InventoryItem
	err   error
}
type searchMsg struct {
	generation int
	query      string
	lines      []string
	err        error
}
type resourceListMsg struct {
	resources []core.MappedResource
	err       error
}
type resourceDetailMsg struct {
	text string
	err  error
}
type sharedFilterTick time.Time

type model struct {
	ctx            context.Context
	cancel         context.CancelFunc
	config         Config
	events         <-chan core.LogEvent
	state          *core.FilterState
	heartbeat      *core.HeartbeatAnalyzer
	input          textinput.Model
	items          []core.InventoryItem
	width          int
	height         int
	selected       int
	scroll         int
	followsLive    bool
	generation     int
	notice         string
	detail         string
	heartbeatOpen  bool
	resourceOpen   bool
	resourceDetail string
	resources      []core.MappedResource
	resourceIndex  int
	lastSharedText string
	lastSharedMode bool
	lastTextRev    sharedRevision
	lastModeRev    sharedRevision
	searches       *searchController
	searching      bool
	searchMatches  int
	searchLines    int
	reconnecting   bool
}

type searchController struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (s *searchController) start(parent context.Context) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	return ctx
}

func (s *searchController) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

var (
	headerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selectedStyle    = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#DCE7F7", Dark: "#1F2937"})
	matchStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("11"))
	filterLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	modeStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("12")).Padding(0, 1)
	okStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	warnStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	alertStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	dimStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	keyStyle         = lipgloss.NewStyle().Bold(true)
)

var (
	bracketTimeLevelPattern = regexp.MustCompile(`^\[(\d{2}:\d{2}:\d{2}(?:\.\d+)?)\s+([[:alpha:]]+)\]\s*`)
	plainTimeLevelPattern   = regexp.MustCompile(`^(\d{2}:\d{2}:\d{2}(?:\.\d+)?)\s+\[([[:alpha:]]+)\]\s*`)
	leadingBracketPattern   = regexp.MustCompile(`^\[([^\]]+)\]\s*`)
)

func Run(parent context.Context, config Config) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	events := make(chan core.LogEvent, 1024)
	items := append([]core.InventoryItem(nil), config.Items...)
	go manageStreams(ctx, config, events)

	input := textinput.New()
	input.Prompt = ""
	input.Focus()
	bufferLines := config.BufferLines
	if bufferLines <= 0 {
		bufferLines = core.DefaultBufferLines
	}
	state := core.NewFilterState(bufferLines)
	shared := readShared(config.FilterFile)
	if shared.textValid && shared.text != "" {
		input.SetValue(shared.text)
		state.SetFilter(shared.text)
	}
	state.SetMatchesOnly(shared.modeValid && shared.mode)
	m := model{
		ctx: ctx, cancel: cancel, config: config, events: events, state: state,
		heartbeat: &core.HeartbeatAnalyzer{}, input: input, items: items,
		selected: -1, followsLive: true,
		lastSharedText: shared.text, lastSharedMode: shared.mode,
		lastTextRev: shared.textRevision, lastModeRev: shared.modeRevision,
		searches: &searchController{},
		searching: strings.TrimSpace(shared.text) != "" && config.Search != nil,
	}
	program := tea.NewProgram(m, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func (m model) Init() tea.Cmd {
	commands := []tea.Cmd{waitForEvent(m.events), sharedTick()}
	if m.config.Search != nil && strings.TrimSpace(m.input.Value()) != "" {
		commands = append(commands, m.searchCommand(m.generation, m.input.Value()))
	}
	return tea.Batch(commands...)
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		inputWidth := msg.Width - 52
		if msg.Width < 80 {
			inputWidth = msg.Width - 30
		}
		m.input.Width = max(10, min(48, inputWidth))
		return m, nil
	case logMsg:
		event := core.LogEvent(msg)
		if event.Err != nil {
			m.notice = event.Err.Error()
			if event.Closed {
				m.notice += "; reconnecting..."
				m.reconnecting = true
			}
		}
		if !event.Closed {
			wasReconnecting := m.reconnecting
			m.reconnecting = false
			if wasReconnecting && strings.HasSuffix(m.notice, "; reconnecting...") {
				m.notice = ""
			}
			m.heartbeat.Add(event.Pod, event.Container, event.Message, event.ObservedAt)
			lines := m.config.Formatter.Format(event.Pod, event.Container, event.Message, true)
			added := m.state.Append(lines...)
			if m.followsLive && added > 0 {
				m.selected = len(m.state.Lines()) - 1
				m.scroll = 0
			}
		}
		return m, waitForEvent(m.events)
	case inventoryMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
		} else {
			m.items = msg.items
		}
		return m, nil
	case searchMsg:
		if msg.generation != m.generation || strings.TrimSpace(msg.query) != strings.TrimSpace(m.input.Value()) {
			return m, nil
		}
		if msg.err != nil {
			m.searching = false
			m.searchMatches = 0
			m.searchLines = 0
			m.notice = "Full history search failed: " + msg.err.Error()
			return m, nil
		}
		if m.state.SetSearchResults(msg.query, msg.lines) {
			m.searching = false
			m.selected = m.state.MatchIndex()
			m.scrollToSelection()
			matches := 0
			needle := strings.ToLower(strings.TrimSpace(msg.query))
			for _, line := range msg.lines {
				if strings.Contains(strings.ToLower(core.StripANSI(line)), needle) {
					matches++
				}
			}
			m.searchMatches = matches
			m.searchLines = len(msg.lines)
			m.notice = ""
		}
		return m, nil
	case resourceListMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
			m.resourceOpen = false
		} else {
			m.resources = msg.resources
			m.resourceIndex = 0
			if len(msg.resources) == 0 {
				m.notice = "No mapped ConfigMaps or Secrets found"
			}
		}
		return m, nil
	case resourceDetailMsg:
		if msg.err != nil {
			m.notice = msg.err.Error()
		} else {
			m.resourceDetail = msg.text
		}
		return m, nil
	case sharedFilterTick:
		shared := readShared(m.config.FilterFile)
		var commands []tea.Cmd
		if shared.textValid && shared.textRevision.newerThan(m.lastTextRev) {
			m.lastTextRev = shared.textRevision
			if shared.text != m.lastSharedText {
				m.lastSharedText = shared.text
				m.input.SetValue(shared.text)
				m.state.SetFilter(shared.text)
				m.generation++
				m.cancelSearch()
				m.searching = strings.TrimSpace(shared.text) != "" && m.config.Search != nil
				m.searchMatches = 0
				m.searchLines = 0
				m.selected = len(m.state.Lines()) - 1
				m.followsLive = true
				if strings.TrimSpace(shared.text) != "" {
					commands = append(commands, m.searchCommand(m.generation, shared.text))
				}
			}
		}
		if shared.modeValid && shared.modeRevision.newerThan(m.lastModeRev) {
			m.lastModeRev = shared.modeRevision
			if shared.mode != m.lastSharedMode {
				m.lastSharedMode = shared.mode
				m.state.SetMatchesOnly(shared.mode)
				m.selected = m.state.MatchIndex()
				m.scrollToSelection()
			}
		}
		commands = append(commands, sharedTick())
		return m, tea.Batch(commands...)
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" || key == "ctrl+q" {
			m.cancel()
			return m, tea.Quit
		}
		if key == "esc" {
			if m.detail != "" {
				m.detail = ""
			} else if m.heartbeatOpen {
				m.heartbeatOpen = false
			} else if m.resourceDetail != "" {
				m.resourceDetail = ""
			} else if m.resourceOpen {
				m.resourceOpen = false
			}
			return m, nil
		}
		if m.heartbeatOpen {
			if key == "f5" {
				m.heartbeatOpen = false
			}
			return m, nil
		}
		if m.resourceOpen {
			return m.updateResourceKey(key)
		}
		if m.detail != "" {
			if key == "enter" {
				m.notice = copyText(m.detail)
				m.detail = ""
			}
			return m, nil
		}
		switch key {
		case "f1":
			if strings.TrimSpace(m.input.Value()) == "" {
				m.notice = "Type a filter before using F1 matches-only mode"
				return m, nil
			}
			mode := m.state.ToggleMatchesOnly()
			revision, err := writeSharedMode(m.config.FilterFile, mode, m.lastModeRev)
			if err != nil {
				m.notice = "Filter mode sync failed: " + err.Error()
			} else {
				m.lastSharedMode = mode
				m.lastModeRev = revision
				m.notice = ""
			}
			m.selected = m.state.MatchIndex()
			m.scrollToSelection()
			return m, nil
		case "f2":
			m.resourceOpen = true
			m.resourceDetail = ""
			m.resources = nil
			m.resourceIndex = 0
			pod := ""
			if len(m.items) > 0 {
				pod = m.items[0].Pod
			}
			if pod == "" || m.config.MappedResources == nil {
				m.notice = "No pod resources are available"
				m.resourceOpen = false
				return m, nil
			}
			return m, m.loadResources(pod)
		case "f5":
			m.heartbeatOpen = true
			return m, nil
		case "up":
			m.moveSelection(-1)
			return m, nil
		case "down":
			m.moveSelection(1)
			return m, nil
		case "pgup":
			m.moveSelection(-m.logHeight())
			return m, nil
		case "pgdown":
			m.moveSelection(m.logHeight())
			return m, nil
		case "home":
			m.selected = 0
			m.followsLive = false
			m.scrollToSelection()
			return m, nil
		case "end":
			m.selected = len(m.state.Lines()) - 1
			m.followsLive = true
			m.scroll = 0
			return m, nil
		case "enter":
			selected := m.state.Selected(m.selected)
			if selected == "" {
				m.notice = "No selected log line"
			} else {
				m.detail = selected
			}
			return m, nil
		}
		before := m.input.Value()
		var command tea.Cmd
		m.input, command = m.input.Update(msg)
		after := m.input.Value()
		if before != after {
			m.state.SetFilter(after)
			m.generation++
			m.cancelSearch()
			m.searching = strings.TrimSpace(after) != "" && m.config.Search != nil
			m.searchMatches = 0
			m.searchLines = 0
			m.selected = len(m.state.Lines()) - 1
			m.followsLive = true
			m.scroll = 0
			revision, err := writeSharedText(m.config.FilterFile, after, m.lastTextRev)
			if err != nil {
				m.notice = "Filter sync failed: " + err.Error()
			} else {
				m.lastSharedText = after
				m.lastTextRev = revision
			}
			if strings.TrimSpace(after) != "" {
				return m, tea.Batch(command, m.searchCommand(m.generation, after))
			}
		}
		return m, command
	}
	return m, nil
}

func (m model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "starting tailg..."
	}
	if m.heartbeatOpen {
		return m.panel("Heartbeat health", core.HeartbeatReport(m.heartbeat.Intervals(m.config.HeartbeatWindow, time.Now())), "F5/Esc closes")
	}
	if m.resourceOpen {
		if m.resourceDetail != "" {
			return m.panel("Mapped resource", m.resourceDetail, "Enter copies | Esc returns")
		}
		var lines []string
		for index, resource := range m.resources {
			prefix := "  "
			if index == m.resourceIndex {
				prefix = "> "
			}
			lines = append(lines, fmt.Sprintf("%s%s %s | %s", prefix, resource.Kind, resource.Name, strings.Join(resource.Sources, ", ")))
		}
		if len(lines) == 0 {
			lines = []string{"Loading mapped resources..."}
		}
		return m.panel("Mapped pod resources", strings.Join(lines, "\n"), "Up/Down select | Enter opens | F2/Esc closes")
	}
	if m.detail != "" {
		return m.panel("Selected log line", m.detail, "Enter copies | Esc closes")
	}
	lines := m.state.Lines()
	height := m.logHeight()
	start := max(0, len(lines)-height-m.scroll)
	end := min(len(lines), start+height)
	visible := make([]string, 0, height)
	for index := start; index < end; index++ {
		visible = append(visible, renderLogRow(lines[index], m.input.Value(), index == m.selected, m.width, m.config.Formatter.ShowPod, m.config.Formatter.Color))
	}
	for len(visible) < height {
		visible = append(visible, "")
	}
	return strings.Join([]string{m.renderHeader(), renderRule(m.width, m.config.Formatter.Color), strings.Join(visible, "\n"), renderRule(m.width, m.config.Formatter.Color), m.renderFilterBar(), m.renderFooter()}, "\n")
}

func (m model) panel(title, body, footer string) string {
	height := max(1, m.height-3)
	lines := strings.Split(body, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	header := renderWithColor(headerStyle, "tailg", m.config.Formatter.Color) + "  " + title
	return strings.Join([]string{truncate(header, m.width), renderRule(m.width, m.config.Formatter.Color), strings.Join(lines, "\n"), truncate(renderWithColor(dimStyle, footer, m.config.Formatter.Color), m.width)}, "\n")
}
func (m model) logHeight() int { return max(1, m.height-6) }

func (m model) renderHeader() string {
	services := strings.Join(core.ServiceNames(m.items), ",")
	if services == "" {
		services = "logs"
	}
	namespace := m.config.Namespace
	if namespace == "" {
		namespace = "default"
	}
	podCount := len(core.UniquePods(m.items))
	podLabel := fmt.Sprintf("%d pods", podCount)
	if podCount == 1 {
		podLabel = "1 pod"
	}
	left := strings.Join([]string{
		renderWithColor(headerStyle, "tailg", m.config.Formatter.Color),
		truncate(services, 32),
		renderWithColor(dimStyle, namespace, m.config.Formatter.Color),
		renderWithColor(dimStyle, podLabel, m.config.Formatter.Color),
	}, "  ")
	if m.width >= 110 && m.config.KubeContext != "" {
		left += "  " + renderWithColor(dimStyle, "context "+m.config.KubeContext, m.config.Formatter.Color)
	}

	state := "● LIVE"
	stateStyle := okStyle
	if m.reconnecting {
		state = "↻ RECONNECTING"
		stateStyle = warnStyle
	} else if !m.followsLive {
		state = fmt.Sprintf("Ⅱ PAUSED -%d", m.scroll)
		stateStyle = warnStyle
	}
	return joinSides(left, renderWithColor(stateStyle, state, m.config.Formatter.Color), m.width)
}

func (m model) renderFilterBar() string {
	mode := "CONTEXT"
	if m.state.MatchesOnly() {
		mode = "MATCHES ONLY"
	}
	modeLabel := "[" + mode + "]"
	if m.config.Formatter.Color {
		modeLabel = modeStyle.Render(mode)
	}
	left := renderWithColor(filterLabelStyle, "FILTER", m.config.Formatter.Color) + "  " + m.input.View() + "  " + modeLabel
	right := ""
	if m.width >= 80 {
		right = m.searchStatus()
	}
	return joinSides(left, renderWithColor(dimStyle, right, m.config.Formatter.Color), m.width)
}

func (m model) searchStatus() string {
	if strings.TrimSpace(m.input.Value()) == "" {
		return ""
	}
	if m.searching {
		return "searching history..."
	}
	return fmt.Sprintf("%d matches • %d lines", m.searchMatches, m.searchLines)
}

func (m model) renderFooter() string {
	if m.notice != "" {
		return truncate(renderWithColor(alertStyle, m.notice, m.config.Formatter.Color), m.width)
	}
	if m.width < 80 && m.searchStatus() != "" {
		return truncate(renderWithColor(dimStyle, m.searchStatus(), m.config.Formatter.Color), m.width)
	}
	shortcuts := []string{
		renderKey("F1", "mode", m.config.Formatter.Color),
		renderKey("F2", "resources", m.config.Formatter.Color),
		m.renderHeartbeatKey(),
		renderKey("Enter", "inspect", m.config.Formatter.Color),
		renderKey("Ctrl+Q", "quit", m.config.Formatter.Color),
	}
	return truncate(strings.Join(shortcuts, "  "), m.width)
}

func (m model) renderHeartbeatKey() string {
	severity := "UNKNOWN"
	if m.heartbeat != nil {
		severity = m.heartbeat.Severity(m.config.HeartbeatWindow)
	}
	key := "F5"
	if m.config.Formatter.Color {
		key = styleSeverity(severity).Render(key)
	}
	return key + " " + renderWithColor(dimStyle, "heartbeat ["+severity+"]", m.config.Formatter.Color)
}

type logColumns struct {
	time    string
	level   string
	pod     string
	message string
}

func parseLogColumns(value string, showPod bool) logColumns {
	remaining := strings.TrimSpace(core.StripANSI(value))
	columns := logColumns{}
	if showPod {
		if match := leadingBracketPattern.FindStringSubmatchIndex(remaining); len(match) == 4 {
			columns.pod = remaining[match[2]:match[3]]
			remaining = strings.TrimSpace(remaining[match[1]:])
		}
	}
	if match := bracketTimeLevelPattern.FindStringSubmatchIndex(remaining); len(match) == 6 {
		columns.time = remaining[match[2]:match[3]]
		columns.level = normalizeLevel(remaining[match[4]:match[5]])
		remaining = strings.TrimSpace(remaining[match[1]:])
	} else if match := plainTimeLevelPattern.FindStringSubmatchIndex(remaining); len(match) == 6 {
		columns.time = remaining[match[2]:match[3]]
		columns.level = normalizeLevel(remaining[match[4]:match[5]])
		remaining = strings.TrimSpace(remaining[match[1]:])
	}
	columns.message = remaining
	return columns
}

func normalizeLevel(level string) string {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "ERROR", "FATAL", "CRIT", "CRITICAL":
		return "ERR"
	case "WARN", "WARNING":
		return "WRN"
	case "INFO", "INFORMATION":
		return "INF"
	case "DEBUG":
		return "DBG"
	case "VERBOSE":
		return "VRB"
	case "TRACE":
		return "TRC"
	default:
		return strings.ToUpper(strings.TrimSpace(level))
	}
}

func renderLogRow(value, query string, selected bool, width int, showPod, color bool) string {
	columns := parseLogColumns(value, showPod)
	gutter := "  "
	if selected {
		gutter = "> "
		if color {
			gutter = headerStyle.Render("▌ ")
		}
	}
	prefix := gutter
	if width >= 60 {
		prefix += renderCell(columns.time, 13, dimStyle, color && !selected)
		prefix += renderCell(columns.level, 5, levelRenderStyle(columns.level), color && !selected)
	}
	if showPod && width >= 96 {
		prefix += renderCell(columns.pod, 18, dimStyle, color && !selected)
	}
	messageWidth := max(1, width-lipgloss.Width(prefix))
	message := truncatePlain(columns.message, messageWidth)
	errorLine := strings.Contains(value, "\x1b[31m") && columns.level == ""
	renderedMessage := highlightText(message, query, color, errorLine && !selected)
	row := prefix + renderedMessage
	if selected && color {
		row = selectedStyle.Width(max(1, width)).Render(row)
	}
	return truncate(row, width)
}

func renderCell(value string, width int, style lipgloss.Style, color bool) string {
	plain := truncatePlain(value, max(1, width-1))
	rendered := renderWithColor(style, plain, color)
	return rendered + strings.Repeat(" ", max(1, width-lipgloss.Width(plain)))
}

func levelRenderStyle(level string) lipgloss.Style {
	switch normalizeLevel(level) {
	case "ERR":
		return alertStyle
	case "WRN":
		return warnStyle
	case "INF":
		return headerStyle
	default:
		return dimStyle
	}
}

func highlightText(value, query string, color, errorLine bool) string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || !color {
		if errorLine && color {
			return alertStyle.Render(value)
		}
		return value
	}
	lower := strings.ToLower(value)
	var builder strings.Builder
	start := 0
	for {
		index := strings.Index(lower[start:], query)
		if index < 0 {
			remaining := value[start:]
			if errorLine {
				remaining = alertStyle.Render(remaining)
			}
			builder.WriteString(remaining)
			break
		}
		index += start
		before := value[start:index]
		if errorLine {
			before = alertStyle.Render(before)
		}
		builder.WriteString(before)
		builder.WriteString(matchStyle.Render(value[index : index+len(query)]))
		start = index + len(query)
	}
	return builder.String()
}

func renderKey(key, label string, color bool) string {
	return renderWithColor(keyStyle, key, color) + " " + renderWithColor(dimStyle, label, color)
}

func renderRule(width int, color bool) string {
	return renderWithColor(dimStyle, strings.Repeat("─", max(1, width)), color)
}

func renderWithColor(style lipgloss.Style, value string, color bool) string {
	if !color {
		return value
	}
	return style.Render(value)
}

func joinSides(left, right string, width int) string {
	if right == "" {
		return truncate(left, width)
	}
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	if leftWidth+rightWidth+1 > width {
		left = truncate(left, max(1, width-rightWidth-1))
		leftWidth = lipgloss.Width(left)
	}
	spaces := max(1, width-leftWidth-rightWidth)
	return truncate(left+strings.Repeat(" ", spaces)+right, width)
}

func truncatePlain(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	if width == 1 {
		return "…"
	}
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "…"
}
func (m *model) moveSelection(delta int) {
	count := len(m.state.Lines())
	if count == 0 {
		m.selected = -1
		return
	}
	if m.selected < 0 {
		m.selected = count - 1
	} else {
		m.selected = min(max(0, m.selected+delta), count-1)
	}
	m.followsLive = false
	m.scrollToSelection()
}
func (m *model) scrollToSelection() {
	count := len(m.state.Lines())
	height := m.logHeight()
	if m.selected < 0 || count == 0 {
		m.scroll = 0
		return
	}
	desiredStart := max(0, m.selected-core.SearchContextLines)
	desiredEnd := min(count, desiredStart+height)
	m.scroll = max(0, count-desiredEnd)
}
func (m model) updateResourceKey(key string) (tea.Model, tea.Cmd) {
	if key == "f2" {
		m.resourceOpen = false
		return m, nil
	}
	if key == "esc" {
		if m.resourceDetail != "" {
			m.resourceDetail = ""
		} else {
			m.resourceOpen = false
		}
		return m, nil
	}
	if m.resourceDetail != "" {
		if key == "enter" {
			m.notice = copyText(m.resourceDetail)
			m.resourceDetail = ""
		}
		return m, nil
	}
	switch key {
	case "up":
		m.resourceIndex = max(0, m.resourceIndex-1)
	case "down":
		m.resourceIndex = min(max(0, len(m.resources)-1), m.resourceIndex+1)
	case "enter":
		if len(m.resources) > 0 && m.config.ResourceDetail != nil {
			return m, m.loadResourceDetail(m.resources[m.resourceIndex])
		}
	}
	return m, nil
}
func (m *model) cancelSearch() {
	if m.searches != nil {
		m.searches.stop()
	}
}
func (m *model) searchCommand(generation int, query string) tea.Cmd {
	if m.config.Search == nil {
		return nil
	}
	if m.searches == nil {
		m.searches = &searchController{}
	}
	searchCtx := m.searches.start(m.ctx)
	return func() tea.Msg {
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-searchCtx.Done():
			return nil
		case <-timer.C:
		}
		lines, err := m.config.Search(searchCtx, query)
		if searchCtx.Err() != nil {
			return nil
		}
		return searchMsg{generation: generation, query: query, lines: lines, err: err}
	}
}
func (m model) loadResources(pod string) tea.Cmd {
	return func() tea.Msg {
		resources, err := m.config.MappedResources(m.ctx, pod)
		return resourceListMsg{resources, err}
	}
}
func (m model) loadResourceDetail(resource core.MappedResource) tea.Cmd {
	return func() tea.Msg {
		text, err := m.config.ResourceDetail(m.ctx, resource)
		return resourceDetailMsg{text, err}
	}
}
func waitForEvent(events <-chan core.LogEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return nil
		}
		return logMsg(event)
	}
}
func sharedTick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return sharedFilterTick(t) })
}

type managedStream struct {
	item       core.InventoryItem
	cancel     context.CancelFunc
	generation uint64
	attempt    int
}

type managedStreamDone struct {
	key        string
	generation uint64
	attempt    int
	lifetime   time.Duration
}

type managedStreamRetry struct {
	key        string
	generation uint64
}

const (
	streamRetryBase  = 250 * time.Millisecond
	streamRetryLimit = 5 * time.Second
	stableStreamTime = 30 * time.Second
)

func streamRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := streamRetryBase
	for step := 1; step < attempt && delay < streamRetryLimit; step++ {
		delay *= 2
	}
	return min(delay, streamRetryLimit)
}

func manageStreams(ctx context.Context, config Config, events chan<- core.LogEvent) {
	defer close(events)
	if config.Stream == nil {
		return
	}

	done := make(chan managedStreamDone)
	retries := make(chan managedStreamRetry)
	active := map[string]*managedStream{}
	wanted := map[string]core.InventoryItem{}
	var streams sync.WaitGroup
	var generation uint64

	start := func(item core.InventoryItem, attempt int) {
		generation++
		streamCtx, cancel := context.WithCancel(ctx)
		handle := &managedStream{item: item, cancel: cancel, generation: generation, attempt: attempt}
		active[item.Key()] = handle
		streams.Add(1)
		go func() {
			defer streams.Done()
			started := time.Now()
			_ = config.Stream(streamCtx, item, events)
			completion := managedStreamDone{key: item.Key(), generation: handle.generation, attempt: handle.attempt, lifetime: time.Since(started)}
			select {
			case done <- completion:
			case <-ctx.Done():
			}
		}()
	}

	reconcile := func(items []core.InventoryItem) {
		current := make(map[string]core.InventoryItem, len(items))
		for _, item := range items {
			key := item.Key()
			current[key] = item
			if _, ok := active[key]; !ok {
				start(item, 0)
			}
		}
		wanted = current
		for key, handle := range active {
			if _, ok := current[key]; ok {
				continue
			}
			if handle.cancel != nil {
				handle.cancel()
			}
			delete(active, key)
		}
	}

	stopAll := func() {
		for _, handle := range active {
			if handle.cancel != nil {
				handle.cancel()
			}
		}
		streams.Wait()
	}

	reconcile(config.Items)
	var inventoryTick <-chan time.Time
	var ticker *time.Ticker
	if config.Inventory != nil {
		interval := config.RefreshInterval
		if interval <= 0 {
			interval = 2 * time.Second
		}
		ticker = time.NewTicker(interval)
		inventoryTick = ticker.C
		defer ticker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			stopAll()
			return
		case completion := <-done:
			handle, ok := active[completion.key]
			if !ok || handle.generation != completion.generation {
				continue
			}
			if _, ok := wanted[completion.key]; !ok {
				delete(active, completion.key)
				continue
			}
			nextAttempt := completion.attempt + 1
			if completion.lifetime >= stableStreamTime {
				nextAttempt = 1
			}
			handle.cancel()
			handle.cancel = nil
			handle.attempt = nextAttempt
			delay := streamRetryDelay(nextAttempt)
			go func(request managedStreamRetry) {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
					select {
					case retries <- request:
					case <-ctx.Done():
					}
				case <-ctx.Done():
				}
			}(managedStreamRetry{key: completion.key, generation: completion.generation})
		case retry := <-retries:
			handle, ok := active[retry.key]
			if !ok || handle.generation != retry.generation || handle.cancel != nil {
				continue
			}
			item, ok := wanted[retry.key]
			if !ok {
				delete(active, retry.key)
				continue
			}
			start(item, handle.attempt)
		case <-inventoryTick:
			items, err := config.Inventory(ctx)
			if err == nil {
				reconcile(items)
			}
		}
	}
}

const (
	sharedFilterPrefix = "tailg-filter-v2:"
	sharedModePrefix   = "tailg-mode-v2:"
)

type sharedRevision struct {
	timestamp int64
	writer    int
}

func (r sharedRevision) newerThan(other sharedRevision) bool {
	return r.timestamp > other.timestamp || (r.timestamp == other.timestamp && r.writer > other.writer)
}

func nextSharedRevision(last sharedRevision) sharedRevision {
	timestamp := time.Now().UnixNano()
	if timestamp <= last.timestamp {
		timestamp = last.timestamp + 1
	}
	return sharedRevision{timestamp: timestamp, writer: os.Getpid()}
}

type sharedSnapshot struct {
	text         string
	textRevision sharedRevision
	textValid    bool
	mode         bool
	modeRevision sharedRevision
	modeValid    bool
}

func InitializeSharedFilter(path string) error {
	if _, err := writeSharedText(path, "", sharedRevision{}); err != nil {
		return err
	}
	_, err := writeSharedMode(path, false, sharedRevision{})
	return err
}

func readShared(path string) sharedSnapshot {
	if path == "" {
		return sharedSnapshot{}
	}
	var snapshot sharedSnapshot
	textBytes, textErr := os.ReadFile(path)
	modeBytes, modeErr := os.ReadFile(path + ".mode")
	if textErr == nil {
		snapshot.text, snapshot.textRevision, snapshot.textValid = decodeSharedText(textBytes)
	}
	if modeErr == nil {
		snapshot.mode, snapshot.modeRevision, snapshot.modeValid = decodeSharedMode(modeBytes)
	}
	return snapshot
}
func writeSharedText(path, text string, last sharedRevision) (sharedRevision, error) {
	if path == "" {
		return nextSharedRevision(last), nil
	}
	var revision sharedRevision
	err := withSharedFileLock(path, func() error {
		current := last
		if data, readErr := os.ReadFile(path); readErr == nil {
			_, diskRevision, valid := decodeSharedText(data)
			if valid && diskRevision.newerThan(current) {
				current = diskRevision
			}
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		revision = nextSharedRevision(current)
		return os.WriteFile(path, encodeSharedText(text, revision), 0o600)
	})
	return revision, err
}
func writeSharedMode(path string, enabled bool, last sharedRevision) (sharedRevision, error) {
	if path == "" {
		return nextSharedRevision(last), nil
	}
	modePath := path + ".mode"
	var revision sharedRevision
	err := withSharedFileLock(modePath, func() error {
		current := last
		if data, readErr := os.ReadFile(modePath); readErr == nil {
			_, diskRevision, valid := decodeSharedMode(data)
			if valid && diskRevision.newerThan(current) {
				current = diskRevision
			}
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		revision = nextSharedRevision(current)
		return os.WriteFile(modePath, encodeSharedMode(enabled, revision), 0o600)
	})
	return revision, err
}
func withSharedFileLock(path string, action func() error) error {
	lockPath := path + ".lock"
	deadline := time.Now().Add(time.Second)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if closeErr := lock.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return closeErr
			}
			actionErr := action()
			removeErr := os.Remove(lockPath)
			if actionErr != nil {
				return actionErr
			}
			return removeErr
		}
		if !os.IsExist(err) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 5*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for shared filter lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func encodeSharedText(text string, revision sharedRevision) []byte {
	return encodeSharedValue(sharedFilterPrefix, base64.StdEncoding.EncodeToString([]byte(text)), revision)
}
func decodeSharedText(data []byte) (string, sharedRevision, bool) {
	payload, revision, valid := decodeSharedValue(sharedFilterPrefix, data)
	if !valid {
		return "", sharedRevision{}, false
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	return string(decoded), revision, err == nil
}
func encodeSharedMode(enabled bool, revision sharedRevision) []byte {
	mode := "context"
	if enabled {
		mode = "matches"
	}
	return encodeSharedValue(sharedModePrefix, mode, revision)
}
func decodeSharedMode(data []byte) (bool, sharedRevision, bool) {
	payload, revision, valid := decodeSharedValue(sharedModePrefix, data)
	if !valid {
		return false, sharedRevision{}, false
	}
	switch payload {
	case "context":
		return false, revision, true
	case "matches":
		return true, revision, true
	default:
		return false, sharedRevision{}, false
	}
}
func encodeSharedValue(prefix, payload string, revision sharedRevision) []byte {
	return []byte(fmt.Sprintf("%s%d:%d:%s\n", prefix, revision.timestamp, revision.writer, payload))
}
func decodeSharedValue(prefix string, data []byte) (string, sharedRevision, bool) {
	value := string(data)
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "\n") {
		return "", sharedRevision{}, false
	}
	parts := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(value, prefix), "\n"), ":", 3)
	if len(parts) != 3 {
		return "", sharedRevision{}, false
	}
	timestamp, timestampErr := strconv.ParseInt(parts[0], 10, 64)
	writer, writerErr := strconv.Atoi(parts[1])
	if timestampErr != nil || writerErr != nil || timestamp <= 0 || writer <= 0 {
		return "", sharedRevision{}, false
	}
	return parts[2], sharedRevision{timestamp: timestamp, writer: writer}, true
}
func copyText(text string) string {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("clip")
	case "darwin":
		command = exec.Command("pbcopy")
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			command = exec.Command("wl-copy")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			command = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	if command == nil {
		return "Clipboard command not found"
	}
	command.Stdin = strings.NewReader(text)
	if err := command.Run(); err != nil {
		return "Copy failed: " + err.Error()
	}
	return fmt.Sprintf("Copied %d line(s)", len(strings.Split(text, "\n")))
}
func truncate(value string, width int) string {
	plain := core.StripANSI(value)
	if lipgloss.Width(plain) <= width {
		return value
	}
	runes := []rune(plain)
	if width <= 1 {
		return ""
	}
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "…"
}
func styleSeverity(severity string) lipgloss.Style {
	switch severity {
	case "OK":
		return okStyle
	case "WARN":
		return warnStyle
	case "ALERT":
		return alertStyle
	default:
		return dimStyle
	}
}
