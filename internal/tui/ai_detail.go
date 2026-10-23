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
	m.aiView = true
	m.detailOffset = 0
	if m.config.AI == nil {
		m.aiText = "AI is not configured. Set TAILG_AI_API_KEY and TAILG_AI_MODEL in your environment.\n" +
			"Optionally pass --ai-repo /path/to/checkout (repeatable).\n\n" +
			"Press A or Esc to return to the original log.\n" +
			"An API request is only made when you press A with AI configured."
		return m, nil
	}
	if m.aiText != "" {
		// Reopen a previously fetched answer without using another paid request.
		return m, nil
	}
	if m.detailLoading {
		// Do not cache the warning as an AI result. The next A after the
		// full error loads should start the actual request.
		m.aiView = false
		return m, nil
	}
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
		result, err := client.Explain(ctx, log)
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
		m.aiText = "AI explanation could not be loaded: " + core.StripANSI(message.err.Error()) + "\n\nPress A to return to the log."
		return m, nil
	}
	var text strings.Builder
	text.WriteString(core.StripANSI(message.result.Explanation))
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
