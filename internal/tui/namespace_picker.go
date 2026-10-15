package tui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const visibleNamespaces = 15

type namespacePickerModel struct {
	namespaces []string
	index      int
	selected   string
	loadError  string
	entering   bool
	expanded   bool
	done       bool
	input      textinput.Model
}

// A manually entered namespace remains available when RBAC denies listing.
func newNamespacePickerModel(namespaces []string, current string, listErr error, usage map[string]ChoiceUsage) namespacePickerModel {
	input := textinput.New()
	input.Prompt = "Namespace: "
	input.Placeholder = "type a namespace name"
	model := namespacePickerModel{namespaces: namespacePickerChoices(namespaces, current, listErr, usage), input: input}
	if listErr != nil {
		model.loadError = pickerErrorSummary(listErr)
	}
	for index, name := range model.namespaces {
		if name == current {
			model.index = index
			break
		}
	}
	return model
}

func namespacePickerChoices(namespaces []string, current string, listErr error, usage map[string]ChoiceUsage) []string {
	candidates := append([]string(nil), namespaces...)
	if listErr != nil {
		// Past choices remain useful if RBAC denies cluster-wide listing.
		for name := range usage {
			candidates = append(candidates, name)
		}
	}
	return rankPickerNames(prepareNamespaces(candidates, current), current, usage)
}

func prepareNamespaces(namespaces []string, current string) []string {
	unique := map[string]bool{}
	for _, name := range append(append([]string(nil), namespaces...), current) {
		if name != "" {
			unique[name] = true
		}
	}
	result := make([]string, 0, len(unique))
	for name := range unique {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (m namespacePickerModel) Init() tea.Cmd { return nil }

func (m namespacePickerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyMsg); ok {
		if m.entering {
			switch key.String() {
			case "ctrl+c":
				m.done = true
				return m, nil
			case "esc":
				m.entering = false
				m.input.Blur()
				return m, nil
			case "enter":
				if name := strings.TrimSpace(m.input.Value()); name != "" {
					m.selected = name
					m.done = true
					return m, nil
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(message)
			return m, cmd
		}
		switch key.String() {
		case "ctrl+c", "q":
			m.done = true
			return m, nil
		case "esc", "b":
			if m.expanded {
				m.expanded = false
				m.index = 0
				return m, nil
			}
			m.done = true
			return m, nil
		case "/":
			m.entering = true
			return m, m.input.Focus()
		case "up", "k":
			m.index = max(0, m.index-1)
		case "down", "j":
			if len(m.namespaces) > 0 {
				m.index = min(visibleChoiceCount(len(m.namespaces), m.expanded)-1, m.index+1)
			}
		case "enter":
			if isMoreChoice(m.index, len(m.namespaces), m.expanded) {
				m.expanded = true
				m.index = frequentChoices
				return m, nil
			}
			if len(m.namespaces) > 0 {
				m.selected = m.namespaces[m.index]
				m.done = true
				return m, nil
			}
		}
	}
	if m.entering {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m namespacePickerModel) View() string {
	lines := []string{
		headerStyle.Render("Switch Kubernetes namespace"),
		renderPickerShortcuts(
			shortcut("↑↓", "Move"),
			shortcut("ENTER", "Switch"),
			shortcut("/", "Enter name"),
			shortcut("ESC", "Applications"),
		),
		"",
	}
	if m.expanded {
		lines[1] = renderPickerShortcuts(
			shortcut("↑↓", "Move through all"),
			shortcut("ENTER", "Switch"),
			shortcut("/", "Enter name"),
			shortcut("ESC", "Frequent"),
		)
	}
	if m.loadError != "" {
		lines = append(lines, "Namespace list unavailable: "+m.loadError, "")
	}
	if m.entering {
		lines = append(lines, m.input.View(), renderPickerShortcuts(
			shortcut("ENTER", "Use name"),
			shortcut("ESC", "Namespace list"),
		), "")
	}
	if len(m.namespaces) == 0 {
		lines = append(lines, "Press / to enter a namespace name.")
		return strings.Join(lines, "\n")
	}
	limit := visibleChoiceCount(len(m.namespaces), m.expanded)
	start := 0
	if m.expanded && limit > visibleNamespaces {
		start = max(0, m.index-visibleNamespaces/2)
		start = min(start, limit-visibleNamespaces)
	}
	end := min(limit, start+visibleNamespaces)
	for index := start; index < end; index++ {
		marker := "  "
		style := lipgloss.NewStyle()
		if index == m.index {
			marker = "> "
			style = selectedStyle
		}
		if isMoreChoice(index, len(m.namespaces), m.expanded) {
			lines = append(lines, style.Render(marker+"More… ("+strconv.Itoa(len(m.namespaces)-frequentChoices)+" other namespaces)"))
			continue
		}
		lines = append(lines, style.Render(marker+m.namespaces[index]))
	}
	if m.expanded && len(m.namespaces) > visibleNamespaces {
		lines = append(lines, dimStyle.Render("Use Up/Down to browse all namespaces"))
	}
	return strings.Join(lines, "\n")
}
