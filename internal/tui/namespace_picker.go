package tui

import (
	"sort"
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
	input      textinput.Model
}

// PickNamespace allows entering a name even when RBAC denies listing all
// namespaces. Actual access is checked when the app picker loads pods.
func PickNamespace(namespaces []string, current string, listErr error) (selected string, ok bool, err error) {
	input := textinput.New()
	input.Prompt = "Namespace: "
	input.Placeholder = "type a namespace name"
	model := namespacePickerModel{namespaces: prepareNamespaces(namespaces, current), input: input}
	if listErr != nil {
		model.loadError = pickerErrorSummary(listErr)
	}
	for index, name := range model.namespaces {
		if name == current {
			model.index = index
			break
		}
	}
	result, err := tea.NewProgram(model).Run()
	if err != nil {
		return "", false, err
	}
	selected = result.(namespacePickerModel).selected
	return selected, selected != "", nil
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
				return m, tea.Quit
			case "esc":
				m.entering = false
				m.input.Blur()
				return m, nil
			case "enter":
				if name := strings.TrimSpace(m.input.Value()); name != "" {
					m.selected = name
					return m, tea.Quit
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(message)
			return m, cmd
		}
		switch key.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "/":
			m.entering = true
			return m, m.input.Focus()
		case "up", "k":
			m.index = max(0, m.index-1)
		case "down", "j":
			if len(m.namespaces) > 0 {
				m.index = min(len(m.namespaces)-1, m.index+1)
			}
		case "enter":
			if len(m.namespaces) > 0 {
				m.selected = m.namespaces[m.index]
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m namespacePickerModel) View() string {
	lines := []string{
		headerStyle.Render("Switch Kubernetes namespace"),
		dimStyle.Render("Up/Down move | Enter switches | / enter name | Esc returns to applications"),
		"",
	}
	if m.loadError != "" {
		lines = append(lines, "Namespace list unavailable: "+m.loadError, "")
	}
	if m.entering {
		lines = append(lines, m.input.View(), dimStyle.Render("Enter uses this name | Esc returns to list"), "")
	}
	if len(m.namespaces) == 0 {
		lines = append(lines, "Press / to enter a namespace name.")
		return strings.Join(lines, "\n")
	}
	start := max(0, m.index-visibleNamespaces/2)
	start = min(start, max(0, len(m.namespaces)-visibleNamespaces))
	end := min(len(m.namespaces), start+visibleNamespaces)
	for index := start; index < end; index++ {
		marker := "  "
		style := lipgloss.NewStyle()
		if index == m.index {
			marker = "> "
			style = selectedStyle
		}
		lines = append(lines, style.Render(marker+m.namespaces[index]))
	}
	if len(m.namespaces) > visibleNamespaces {
		lines = append(lines, dimStyle.Render("Use Up/Down to browse all namespaces"))
	}
	return strings.Join(lines, "\n")
}
