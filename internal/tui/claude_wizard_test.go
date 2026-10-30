package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fernandocpaz/tailg/internal/ai"
)

func TestClaudeWizardOffersSpecificFieldsAndSkipsEndpoint(t *testing.T) {
	m := workflowModel()
	m.width, m.height = 112, 30
	m.aiSetup = wizardModel(ai.Config{APIKey: "old-openai-key", Model: "old-model", InputUSDPerMillion: 1}, "")
	if !strings.Contains(m.View(), "Claude (Anthropic API)") {
		t.Fatal("Claude provider is missing")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	w := m.aiSetup
	if w.provider != aiProviderClaude || w.fields[aiStepKey] != "" || w.fields[aiStepInputRate] != "" || w.fields[aiStepModel] != ai.DefaultClaudeModel {
		t.Fatal("switch did not set Claude defaults and clear unrelated credentials/prices")
	}
	m, _ = wizardKey(m, "enter")
	w.input.SetValue("claude-secret-must-be-masked")
	view := m.View()
	if strings.Contains(view, "claude-secret-must-be-masked") || !strings.Contains(view, "platform.claude.com/settings/keys") || !strings.Contains(view, "subscription") {
		t.Fatal("missing masked key or Claude account guidance")
	}
	m, _ = wizardKey(m, "enter")
	if w.step != aiStepWorkspace || !strings.Contains(m.View(), "single workspace") {
		t.Fatal("workspace guidance missing")
	}
	w.input.SetValue("wrkspc_example")
	m, _ = wizardKey(m, "enter")
	if w.step != aiStepModel || !strings.Contains(m.View(), ai.AnthropicBaseURL) {
		t.Fatal("native endpoint not shown automatically")
	}
	w.input.SetValue("my-accessible-claude-model")
	m, _ = wizardKey(m, "enter")
	if w.step != aiStepRepositories {
		t.Fatal("Claude should skip manual endpoint entry")
	}
	m, _ = wizardKey(m, "shift+tab")
	if w.step != aiStepModel || w.input.Value() != "my-accessible-claude-model" {
		t.Fatal("back lost model or returned to endpoint")
	}
	w.commitInput()
	cfg, err := w.buildConfig()
	if err != nil || cfg.Provider != ai.ProviderAnthropic || cfg.BaseURL != ai.AnthropicBaseURL || cfg.APIKey != "claude-secret-must-be-masked" || cfg.WorkspaceID != "wrkspc_example" {
		t.Fatalf("wrong Claude configuration: %v", err)
	}
	// All providers are reachable and switching back restores the OpenAI protocol.
	w.selectProvider(aiProviderCompatible)
	w.fields[aiStepEndpoint] = "https://example.com/v1"
	w.fields[aiStepModel] = "custom-model"
	cfg, err = w.buildConfig()
	if err != nil || cfg.Provider != ai.ProviderOpenAI || cfg.WorkspaceID != "" {
		t.Fatal("compatible provider retained Claude settings")
	}
	w.selectProvider(aiProviderOpenAI)
	w.step = aiStepKey
	w.loadInput()
	w.advance()
	if w.step != aiStepModel {
		t.Fatal("OpenAI should skip the Claude workspace step")
	}
	w.back()
	if w.step != aiStepKey {
		t.Fatal("OpenAI back should skip workspace")
	}
}

func TestClaudeWizardValidatesSavesReopensAndExplains(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	queries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "claude-key" {
			t.Error("not a native Claude request")
		}
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if queries == 1 && (len(payload.Messages) != 1 || payload.Messages[0].Content != "Reply OK.") {
			t.Error("validation sent log/source data")
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":10,"output_tokens":2}}`)
	}))
	defer server.Close()
	m := workflowModel()
	m.ctx = context.Background()
	m.width, m.height = 112, 30
	m.openDetail("ERROR selected")
	m.config.AISettings = ai.Config{
		Provider: ai.ProviderAnthropic, APIKey: "claude-key", Model: "chosen-model", BaseURL: server.URL + "/v1",
		ServiceRepos: map[string]ai.ServiceRepository{"api/service": {Path: t.TempDir()}},
	}
	updated, _ := m.openAIWizard("")
	m = updated.(model)
	w := m.aiSetup
	if w.provider != aiProviderClaude {
		t.Fatal("existing Claude settings not recognized")
	}
	w.step = aiStepTest
	m, cmd := wizardKey(m, "enter")
	if cmd == nil {
		t.Fatal("test not started")
	}
	m = m.UpdateModelTest(cmd())
	if w.step != aiStepSave || w.verified == nil {
		t.Fatalf("native validation failed: %s", w.note)
	}
	if _, exists, _ := ai.LoadSettings(); exists {
		t.Fatal("test saved before confirmation")
	}
	m, cmd = wizardKey(m, "enter")
	if m.aiSetup != nil || m.config.AI == nil || cmd == nil {
		t.Fatal("save did not activate Claude")
	}
	stored, exists, err := ai.LoadSettings()
	if err != nil || !exists || stored.Provider != ai.ProviderAnthropic || stored.Model != "chosen-model" || len(stored.ServiceRepos) != 1 {
		t.Fatalf("Claude settings or source mapping lost: %v", err)
	}
	reopened := wizardModel(stored, "")
	if reopened.provider != aiProviderClaude || reopened.fields[aiStepKey] != "claude-key" || reopened.fields[aiStepModel] != "chosen-model" {
		t.Fatal("saved Claude settings did not reopen for editing")
	}
	m = m.UpdateModelTest(cmd())
	if queries != 2 || !m.aiView || !strings.Contains(m.aiText, "OK") {
		t.Fatalf("saved Claude client was not immediately usable: %d %q", queries, m.aiText)
	}
}
