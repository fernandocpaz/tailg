package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fernandocpaz/tailg/internal/kube"
)

type contextPickerModel struct {
	contexts []kube.ContextInfo
	index    int
	selected string
	expanded bool
}

// PickContext returns ok=false when the user cancels. The highlighted entry
// follows tailg's current session context, which may differ from kubectl's.
func PickContext(contexts []kube.ContextInfo, current string, usage map[string]ChoiceUsage) (selected string, ok bool, err error) {
	if len(contexts) == 0 {
		return "", false, fmt.Errorf("no Kubernetes contexts were found in kubeconfig")
	}
	names := make([]string, 0, len(contexts))
	byName := make(map[string]kube.ContextInfo, len(contexts))
	for _, entry := range contexts {
		names = append(names, entry.Name)
		byName[entry.Name] = entry
	}
	model := contextPickerModel{}
	for _, name := range rankPickerNames(names, current, usage) {
		model.contexts = append(model.contexts, byName[name])
	}
	result, err := tea.NewProgram(model).Run()
	if err != nil {
		return "", false, err
	}
	selected = result.(contextPickerModel).selected
	return selected, selected != "", nil
}

func (m contextPickerModel) Init() tea.Cmd { return nil }

func (m contextPickerModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc", "b":
			if m.expanded {
				m.expanded = false
				m.index = 0
				return m, nil
			}
			return m, tea.Quit
		case "up", "k":
			m.index = max(0, m.index-1)
		case "down", "j":
			m.index = min(visibleChoiceCount(len(m.contexts), m.expanded)-1, m.index+1)
		case "enter":
			if isMoreChoice(m.index, len(m.contexts), m.expanded) {
				m.expanded = true
				m.index = frequentChoices
				return m, nil
			}
			m.selected = m.contexts[m.index].Name
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m contextPickerModel) View() string {
	help := "Up/Down move | Enter switches | Esc returns to applications"
	if m.expanded {
		help = "All contexts | Up/Down move | Enter switches | Esc frequent"
	}
	lines := []string{
		headerStyle.Render("Switch Kubernetes context"),
		dimStyle.Render(help),
		"",
		dimStyle.Render(fmt.Sprintf("  %-42s %s", "CONTEXT", "NAMESPACE")),
	}
	limit := visibleChoiceCount(len(m.contexts), m.expanded)
	start := 0
	if m.expanded && limit > visibleNamespaces {
		start = max(0, m.index-visibleNamespaces/2)
		start = min(start, limit-visibleNamespaces)
	}
	end := min(limit, start+visibleNamespaces)
	for i := start; i < end; i++ {
		marker := "  "
		style := lipgloss.NewStyle()
		if i == m.index {
			marker = "> "
			style = selectedStyle
		}
		if isMoreChoice(i, len(m.contexts), m.expanded) {
			lines = append(lines, style.Render(fmt.Sprintf("%sMore… (%d other contexts)", marker, len(m.contexts)-frequentChoices)))
			continue
		}
		entry := m.contexts[i]
		name := truncatePickerCell(entry.Name, 42)
		lines = append(lines, style.Render(fmt.Sprintf("%s%-42s %s", marker, name, entry.Namespace)))
	}
	if m.expanded && limit > visibleNamespaces {
		lines = append(lines, dimStyle.Render("Use Up/Down to browse all contexts"))
	}
	return strings.Join(lines, "\n")
}
