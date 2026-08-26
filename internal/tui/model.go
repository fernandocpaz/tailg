package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernandocpaz/tailg/internal/core"
)

type Config struct {
	Title           string
	Items           []core.InventoryItem
	Formatter       core.Formatter
	HeartbeatWindow time.Duration
	RefreshInterval time.Duration
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
}

var (
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	okStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	warnStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	alertStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func Run(parent context.Context, config Config) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	events := make(chan core.LogEvent, 1024)
	items := append([]core.InventoryItem(nil), config.Items...)
	go manageStreams(ctx, config, events)

	input := textinput.New()
	input.Prompt = "Filter: "
	input.Focus()
	state := core.NewFilterState(0)
	sharedText, sharedMode, _ := readShared(config.FilterFile)
	if sharedText != "" {
		input.SetValue(sharedText)
		state.SetFilter(sharedText)
	}
	state.SetMatchesOnly(sharedMode)
	m := model{
		ctx: ctx, cancel: cancel, config: config, events: events, state: state,
		heartbeat: &core.HeartbeatAnalyzer{}, input: input, items: items,
		selected: -1, followsLive: true, lastSharedText: sharedText, lastSharedMode: sharedMode,
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
		m.input.Width = max(10, msg.Width-9)
		return m, nil
	case logMsg:
		event := core.LogEvent(msg)
		if event.Err != nil && !event.Closed {
			m.notice = event.Err.Error()
		}
		if !event.Closed {
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
			m.notice = "Full history search failed: " + msg.err.Error()
			return m, nil
		}
		if m.state.SetSearchResults(msg.query, msg.lines) {
			m.selected = m.state.MatchIndex()
			m.scrollToSelection()
			matches := 0
			needle := strings.ToLower(strings.TrimSpace(msg.query))
			for _, line := range msg.lines {
				if strings.Contains(strings.ToLower(core.StripANSI(line)), needle) {
					matches++
				}
			}
			if m.state.MatchesOnly() {
				m.notice = fmt.Sprintf("Loaded %d matching log lines", matches)
			} else {
				m.notice = fmt.Sprintf("Loaded %d lines from earliest match (%d matches)", len(msg.lines), matches)
			}
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
		text, mode, valid := readShared(m.config.FilterFile)
		var commands []tea.Cmd
		if valid {
			if text != m.lastSharedText {
				m.lastSharedText = text
				m.input.SetValue(text)
				m.state.SetFilter(text)
				m.generation++
				m.selected = len(m.state.Lines()) - 1
				m.followsLive = true
				if strings.TrimSpace(text) != "" {
					commands = append(commands, m.searchCommand(m.generation, text))
				}
			}
			if mode != m.lastSharedMode {
				m.lastSharedMode = mode
				m.state.SetMatchesOnly(mode)
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
			m.lastSharedMode = mode
			writeSharedMode(m.config.FilterFile, mode)
			m.selected = m.state.MatchIndex()
			m.scrollToSelection()
			m.notice = fmt.Sprintf("F1 matches only [%s]", map[bool]string{true: "ON", false: "OFF"}[mode])
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
			m.selected = len(m.state.Lines()) - 1
			m.followsLive = true
			m.scroll = 0
			m.lastSharedText = after
			writeSharedText(m.config.FilterFile, after)
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
		line := lines[index]
		if index == m.selected {
			line = selectedStyle.Render(core.StripANSI(line))
		}
		visible = append(visible, truncate(line, m.width))
	}
	for len(visible) < height {
		visible = append(visible, "")
	}
	heartbeatSeverity := m.heartbeat.Severity(m.config.HeartbeatWindow)
	mode := "OFF"
	if m.state.MatchesOnly() {
		mode = "ON"
	}
	live := "LIVE"
	if !m.followsLive {
		live = fmt.Sprintf("SCROLLED -%d", m.scroll)
	}
	status := fmt.Sprintf("F1 matches only [%s] | F2 pod resources | %s | %s", mode, styleSeverity(heartbeatSeverity).Render("F5 heartbeat ["+heartbeatSeverity+"]"), live)
	if m.notice != "" {
		status = m.notice
	}
	return strings.Join([]string{headerStyle.Render(m.config.Title), strings.Repeat("-", max(1, m.width)), strings.Join(visible, "\n"), strings.Repeat("-", max(1, m.width)), m.input.View(), truncate(status, m.width)}, "\n")
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
	return strings.Join([]string{headerStyle.Render(title), strings.Repeat("-", max(1, m.width)), strings.Join(lines, "\n"), dimStyle.Render(footer)}, "\n")
}
func (m model) logHeight() int { return max(1, m.height-6) }
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
func (m model) searchCommand(generation int, query string) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
			return nil
		case <-time.After(250 * time.Millisecond):
		}
		lines, err := m.config.Search(m.ctx, query)
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

func manageStreams(ctx context.Context, config Config, events chan<- core.LogEvent) {
	defer close(events)
	active := map[string]context.CancelFunc{}
	reconcile := func(items []core.InventoryItem) {
		current := map[string]bool{}
		for _, item := range items {
			current[item.Key()] = true
			if _, ok := active[item.Key()]; !ok {
				streamCtx, cancel := context.WithCancel(ctx)
				active[item.Key()] = cancel
				go config.Stream(streamCtx, item, events)
			}
		}
		for key, cancel := range active {
			if !current[key] {
				cancel()
				delete(active, key)
			}
		}
	}
	reconcile(config.Items)
	if config.Inventory == nil {
		<-ctx.Done()
		for _, cancel := range active {
			cancel()
		}
		return
	}
	interval := config.RefreshInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, cancel := range active {
				cancel()
			}
			return
		case <-ticker.C:
			items, err := config.Inventory(ctx)
			if err == nil {
				reconcile(items)
			}
		}
	}
}

const (
	sharedFilterPrefix = "tailg-filter-v1:"
	sharedModePrefix   = "tailg-mode-v1:"
)

func InitializeSharedFilter(path string) error {
	if err := os.WriteFile(path, encodeSharedText(""), 0o600); err != nil {
		return err
	}
	return os.WriteFile(path+".mode", encodeSharedMode(false), 0o600)
}

func readShared(path string) (string, bool, bool) {
	if path == "" {
		return "", false, true
	}
	textBytes, textErr := os.ReadFile(path)
	modeBytes, modeErr := os.ReadFile(path + ".mode")
	if textErr != nil || modeErr != nil {
		return "", false, false
	}
	text, textValid := decodeSharedText(textBytes)
	mode, modeValid := decodeSharedMode(modeBytes)
	return text, mode, textValid && modeValid
}
func writeSharedText(path, text string) {
	if path != "" {
		_ = os.WriteFile(path, encodeSharedText(text), 0o600)
	}
}
func writeSharedMode(path string, enabled bool) {
	if path == "" {
		return
	}
	_ = os.WriteFile(path+".mode", encodeSharedMode(enabled), 0o600)
}
func encodeSharedText(text string) []byte {
	return []byte(sharedFilterPrefix + base64.StdEncoding.EncodeToString([]byte(text)) + "\n")
}
func decodeSharedText(data []byte) (string, bool) {
	value := string(data)
	if !strings.HasPrefix(value, sharedFilterPrefix) || !strings.HasSuffix(value, "\n") {
		return "", false
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(value, sharedFilterPrefix), "\n")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	return string(decoded), err == nil
}
func encodeSharedMode(enabled bool) []byte {
	mode := "context"
	if enabled {
		mode = "matches"
	}
	return []byte(sharedModePrefix + mode + "\n")
}
func decodeSharedMode(data []byte) (bool, bool) {
	switch string(data) {
	case sharedModePrefix + "context\n":
		return false, true
	case sharedModePrefix + "matches\n":
		return true, true
	default:
		return false, false
	}
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
