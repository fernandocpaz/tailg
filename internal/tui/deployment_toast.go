package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fernandocpaz/tailg/internal/core"
)

const (
	deploymentToastDuration = 8 * time.Second
	maxDeploymentToasts     = 3
)

type deploymentVersion struct {
	deployedAt time.Time
	imageTag   string
}

type deploymentToast struct {
	id      int
	name    string
	detail  string
	at      time.Time
}

type deploymentToastExpiredMsg struct {
	id         int
	generation int
}

// The full inventory (not the newest-20 visible rows) is the baseline.
// ReplicaSet creation time stays constant when an ordinary pod restarts,
// but advances when a Deployment creates a new revision, even with the same tag.
func deploymentVersions(apps []core.AppChoice) map[string]deploymentVersion {
	versions := make(map[string]deploymentVersion, len(apps))
	for _, app := range apps {
		if app.CronJob != nil || len(app.Pods) == 0 || app.Name == "" {
			continue
		}
		versions[app.Name] = deploymentVersion{deployedAt: app.DeployedAt, imageTag: app.ImageTag}
	}
	return versions
}

func (m *pickerModel) beginDeploymentNotifications() {
	apps := m.allApps
	if apps == nil {
		apps = m.apps
	}
	m.deploymentInventory = deploymentVersions(apps)
	m.deploymentToasts = nil
}

func (m *pickerModel) clearDeploymentNotifications() {
	m.deploymentInventory = nil
	m.deploymentToasts = nil
}

// Only a successful inventory refresh may move the baseline. The initial
// inventory and changes made before monitor mode is enabled never alert.
func (m *pickerModel) detectDeployments(apps []core.AppChoice, err error) tea.Cmd {
	if !m.monitorEnabled || (err != nil && len(apps) == 0) {
		return nil
	}
	next := deploymentVersions(apps)
	if m.deploymentInventory == nil {
		m.deploymentInventory = next
		return nil
	}
	var commands []tea.Cmd
	// Use the inventory's order so multiple deployments notify deterministically.
	for _, app := range apps {
		current, exists := next[app.Name]
		if !exists {
			continue
		}
		previous, known := m.deploymentInventory[app.Name]
		detail := ""
		switch {
		case !known:
			detail = "New application"
		case !current.deployedAt.IsZero() && !previous.deployedAt.IsZero() && current.deployedAt.After(previous.deployedAt):
			detail = "New rollout"
		case (current.deployedAt.IsZero() || previous.deployedAt.IsZero()) &&
			current.imageTag != previous.imageTag && current.imageTag != "" && previous.imageTag != "":
			// Fall back to image changes when rollout timestamps aren't available.
			detail = "Image changed"
		}
		if detail == "" {
			continue
		}
		if known && previous.imageTag != current.imageTag && previous.imageTag != "" {
			detail += " · " + previous.imageTag + " → " + current.imageTag
		} else if current.imageTag != "" && current.imageTag != "-" {
			detail += " · image " + current.imageTag
		}
		m.nextDeploymentToastID++
		toast := deploymentToast{id: m.nextDeploymentToastID, name: app.Name, detail: detail, at: time.Now()}
		m.deploymentToasts = append(m.deploymentToasts, toast)
		if len(m.deploymentToasts) > maxDeploymentToasts {
			m.deploymentToasts = m.deploymentToasts[len(m.deploymentToasts)-maxDeploymentToasts:]
		}
		id, generation := toast.id, m.monitorGeneration
		commands = append(commands, tea.Tick(deploymentToastDuration, func(time.Time) tea.Msg {
			return deploymentToastExpiredMsg{id: id, generation: generation}
		}))
	}
	m.deploymentInventory = next
	if len(commands) == 0 {
		return nil
	}
	return tea.Batch(commands...)
}

func (m *pickerModel) expireDeploymentToast(msg deploymentToastExpiredMsg) {
	if !m.monitorEnabled || msg.generation != m.monitorGeneration {
		return
	}
	for i, toast := range m.deploymentToasts {
		if toast.id == msg.id {
			m.deploymentToasts = append(m.deploymentToasts[:i], m.deploymentToasts[i+1:]...)
			break
		}
	}
}

// Non-modal toast overlay: terminal cells preserve ANSI styling and wide
// characters, and the picker retains its normal cursor and key handling.
func (m pickerModel) overlayDeploymentToasts(background string) string {
	if !m.monitorEnabled || len(m.deploymentToasts) == 0 {
		return background
	}
	width, height := m.width, m.height
	if width <= 0 {
		width = 160
	}
	if height <= 0 {
		height = len(strings.Split(background, "\n"))
	}
	if width < 32 || height < 12 {
		return background
	}

	lines := strings.Split(background, "\n")
	cardWidth := min(68, width-4)
	contentWidth := cardWidth - 4 // rounded border and one cell padding per side
	y := 3
	for i := len(m.deploymentToasts) - 1; i >= 0; i-- {
		toast := m.deploymentToasts[i]
		title := fmt.Sprintf("NEW DEPLOYMENT  ·  %s", toast.at.Format("15:04:05"))
		body := strings.Join([]string{
			warnStyle.Render(ansi.Truncate(title, contentWidth, "…")),
			headerStyle.Render(ansi.Truncate(toast.name, contentWidth, "…")),
			dimStyle.Render(ansi.Truncate(m.kubeContext+" / "+m.namespace, contentWidth, "…")),
			ansi.Truncate(toast.detail, contentWidth, "…"),
		}, "\n")
		card := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(accentColor).Padding(0, 1).
			Width(contentWidth).Render(body)
		front := strings.Split(card, "\n")
		w := min(width, lipgloss.Width(card))
		x := max(0, width-w-1)
		if y+len(front) > height-2 {
			break
		}
		for j, line := range front {
			row := y + j
			if row >= len(lines) {
				break
			}
			lines[row] = modalCells(lines[row], 0, x) +
				modalCells(line, 0, w) +
				modalCells(lines[row], x+w, width)
		}
		y += len(front) + 1
	}
	return strings.Join(lines, "\n")
}
