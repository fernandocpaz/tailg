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
}

// PickContext returns ok=false when the user cancels. The highlighted entry
// follows tailg's current session context, which may differ from kubectl's.
func PickContext(contexts []kube.ContextInfo, current string) (selected string, ok bool, err error) {
	if len(contexts) == 0 {
		return "", false, fmt.Errorf("no Kubernetes contexts were found in kubeconfig")
	}
	model := contextPickerModel{contexts: contexts}
	for i, entry := range contexts {
		if entry.Name == current {
			model.index = i
			break
		}
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
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			m.index = max(0, m.index-1)
		case "down", "j":
			m.index = min(len(m.contexts)-1, m.index+1)
		case "enter":
			m.selected = m.contexts[m.index].Name
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m contextPickerModel) View() string {
	lines := []string{
		headerStyle.Render("Switch Kubernetes context"),
		dimStyle.Render("Up/Down move | Enter switches | Esc returns to applications"),
		"",
		dimStyle.Render(fmt.Sprintf("  %-42s %s", "CONTEXT", "NAMESPACE")),
	}
	for i, entry := range m.contexts {
		marker := "  "
		style := lipgloss.NewStyle()
		if i == m.index {
			marker = "> "
			style = selectedStyle
		}
		name := truncatePickerCell(entry.Name, 42)
		lines = append(lines, style.Render(fmt.Sprintf("%s%-42s %s", marker, name, entry.Namespace)))
	}
	return strings.Join(lines, "\n")
}
