package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernandocpaz/tailg/internal/ai"
	"github.com/fernandocpaz/tailg/internal/core"
)

type aiExplanationMsg struct {
	generation int
	result ai.Result
	err error
}

// AI is deliberately on-demand: no remote request occurs until A is pressed.
// A second A returns to the original log. Closing the inspector cancels the request.
func (m model) toggleAIExplanation() (tea.Model, tea.Cmd) {
	if m.aiView {
		m.closeAIView()
		return m, nil
	}
	if m.config.AI == nil {
		return m.openAIWizard("AI provider is not configured. The wizard will help you connect it.")
	}
	if m.detailLoading {return m,nil}
	service:=m.selectedAIService()
	if service!="" {
		mapping,ok:=m.config.AISettings.CurrentServiceRepo(service)
		if !ok {
			return m.openServiceRepoWizard(service,"No source repository is mapped for this service yet.")
		}
		return m.startAIExplanation([]string{mapping.Path})
	}
	// Legacy callers without a selected Kubernetes workload retain log-only
	// or explicitly configured global source behavior.
	return m.startAIExplanation(m.config.AISettings.Repos)
}

func (m model) startAIExplanation(repos []string) (tea.Model,tea.Cmd){
	m.aiView=true
	m.detailOffset=0
	if m.aiText!=""{return m,nil}
	ctx := m.ctx
	if ctx == nil { ctx = context.Background() }
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	m.cancelAI()
	m.aiGeneration++
	generation := m.aiGeneration
	m.aiCancel = cancel
	m.aiLoading = true
	log := m.detail
	client := m.config.AI
	return m, func() tea.Msg {
		defer cancel()
		var result ai.Result
		var err error
		if service:=m.selectedAIService();service!="" && len(repos)==1 {
			if mapping,ok:=m.config.AISettings.CurrentServiceRepo(service);ok && mapping.Path==repos[0] {
				result,err=client.ExplainWithService(ctx,log,mapping)
			} else {
				result,err=client.ExplainWithRepos(ctx,log,repos)
			}
		} else {
			result,err=client.ExplainWithRepos(ctx,log,repos)
		}
		if ctx.Err() == context.Canceled { return nil }
		return aiExplanationMsg{generation: generation, result: result, err: err}
	}
}

func (m *model) cancelAI() {
	if m.aiCancel != nil {
		m.aiCancel()
		m.aiCancel = nil
	}
	m.aiGeneration++
}

func (m *model) closeAIView() {
	if m.aiLoading {
		m.cancelAI()
		m.aiText = ""
	}
	m.aiView, m.aiLoading = false, false
	m.detailOffset = 0
}

func (m model) updateAIExplanation(message aiExplanationMsg) (tea.Model, tea.Cmd) {
	if m.detail == "" || !m.aiView || message.generation != m.aiGeneration {
		return m, nil
	}
	m.aiLoading = false
	m.aiCancel = nil
	m.detailOffset = 0
	if message.err != nil {
		m.aiView=false
		return m.openAIWizard("AI provider unavailable: "+core.StripANSI(message.err.Error())+" — update or test the connection below.")
	}
	var text strings.Builder
	text.WriteString(core.StripANSI(message.result.Explanation))
	if service:=m.selectedAIService();service!="" {
		if mapping,ok:=m.config.AISettings.CurrentServiceRepo(service);ok {
			if mapping.Branch!="" || mapping.Commit!="" {
				fmt.Fprintf(&text,"\n\nInspected source snapshot: %s",mapping.Branch)
				if len(mapping.Commit)>=8 {fmt.Fprintf(&text," @ %s",mapping.Commit[:8])}
				text.WriteString(" (not verified against deployed image)")
			}
		}
	}
	if len(message.result.MatchedFiles) > 0 {
		fmt.Fprintf(&text,"\n\nSource excerpts provided: %s",strings.Join(message.result.MatchedFiles,", "))
	} else {
		text.WriteString("\n\nNo matching local source excerpts were included.")
	}
	if message.result.RequestHasTokens {
		fmt.Fprintf(&text,"\n\nThis request: %d input + %d output tokens",
			message.result.RequestInputTokens,message.result.RequestOutputTokens)
		if message.result.RequestHasPrice {
			fmt.Fprintf(&text," (estimated $%.4f)",message.result.RequestUSD)
		}
	}
	text.WriteString("\n" + ai.UsageText(message.result.Usage))
	text.WriteString("\n\nThese are reported token totals and estimated local costs, not your provider's actual remaining credit.")
	m.aiText = text.String()
	return m, nil
}
