package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestClaudeValidationAndExplanationUseNativeMessages(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" ||
			r.Header.Get("x-api-key") != "claude-test-key" ||
			r.Header.Get("anthropic-version") != "2026-10-21" ||
			r.Header.Get("anthropic-workspace-id") != "wrkspc_test" ||
			r.Header.Get("Authorization") != "" {
			t.Errorf("incorrect native Claude request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Model     string        `json:"model"`
			System    string        `json:"system"`
			Messages  []chatMessage `json:"messages"`
			MaxTokens int           `json:"max_tokens"`
			Thinking  struct {
				Type string `json:"type"`
			} `json:"thinking"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if payload.Model != "claude-test" || len(payload.Messages) != 1 || payload.Messages[0].Role != "user" || payload.Thinking.Type != "" {
			t.Errorf("incorrect Messages payload: %+v", payload)
			return
		}
		if requests == 1 {
			if payload.System != "" || payload.Messages[0].Content != "Reply OK." || payload.MaxTokens != 8 {
				t.Errorf("validation sent application data: %+v", payload)
			}
			fmt.Fprint(w, `{"content":[{"type":"text","text":"OK"}]}`)
			return
		}
		if !strings.Contains(payload.System, "read-only") || payload.MaxTokens != maxOutputTokens ||
			!strings.Contains(payload.Messages[0].Content, "AuthService") || strings.Contains(payload.Messages[0].Content, "sensitive-token") {
			t.Error("missing safety prompt or log redaction")
		}
		w.Header().Set("anthropic-ratelimit-requests-remaining", "42")
		w.Header().Set("anthropic-ratelimit-tokens-remaining", "8000")
		fmt.Fprint(w, `{"content":[{"type":"thinking","thinking":"private"},{"type":"text","text":"Check credentials."},{"type":"text","text":"Verify the audience."}],"usage":{"input_tokens":100,"output_tokens":20}}`)
	}))
	defer server.Close()
	client, err := New(Config{Provider: ProviderAnthropic, WorkspaceID: "wrkspc_test", APIKey: "claude-test-key", Model: "claude-test", BaseURL: server.URL + "/v1/", InputUSDPerMillion: 1, OutputUSDPerMillion: 2, BudgetUSD: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Explain(context.Background(), "ERROR AuthService Authorization: sensitive-token")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || result.Explanation != "Check credentials.\nVerify the audience." || result.RequestInputTokens != 100 || result.RequestOutputTokens != 20 || !result.RequestHasTokens {
		t.Fatalf("incorrect Claude result: %+v", result)
	}
	if !result.RequestHasPrice || result.RequestUSD < 0.000139 || result.RequestUSD > 0.000141 ||
		result.Usage.RateRemainingRequests != 42 || result.Usage.RateRemainingTokens != 8000 || !result.Usage.HasRateRemainingTokens || result.Usage.RemainingUSD >= 1 {
		t.Fatalf("incorrect Claude usage: %+v", result)
	}
}

func TestClaudeRejectsInvalidResponsesAndRedactsProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"bad-key", 401, "secret echoed by provider", "API key"},
		{"billing-or-workspace", 400, "secret echoed by provider", "workspace ID"},
		{"missing-model", 404, "secret echoed by provider", "model"},
		{"rate-limited", 429, "secret echoed by provider", "quota"},
		{"not-json", 200, "not-json", "invalid Messages"},
		{"openai-format", 200, `{"choices":[{"message":{"content":"OK"}}]}`, "no text"},
		{"thinking-only", 200, `{"content":[{"type":"thinking","text":"not an answer"}]}`, "no text"},
		{"oversized", 200, strings.Repeat("x", 4097), "size limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			defer server.Close()
			client, err := New(Config{Provider: ProviderAnthropic, APIKey: "secret", Model: "test", BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			err = client.Check(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret echoed") {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestClaudeDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		fmt.Fprint(w, `{"content":[{"type":"text","text":"OK"}]}`)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := New(Config{Provider: ProviderAnthropic, APIKey: "secret", Model: "test", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Check(context.Background()); err == nil || targetCalls != 0 {
		t.Fatalf("credentials followed redirect; calls=%d err=%v", targetCalls, err)
	}
}

func TestClaudeValidationAcceptsThinkingModelsButInspectorNeedsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"reasoning"}],"stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":8}}`)
	}))
	defer server.Close()
	client, err := New(Config{Provider: ProviderAnthropic, APIKey: "key", Model: "thinking-model", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("working thinking model rejected: %v", err)
	}
	if _, err := client.Explain(context.Background(), "ERROR selected"); err == nil || !strings.Contains(err.Error(), "no visible answer") {
		t.Fatalf("inspector accepted an empty answer: %v", err)
	}
	if usage := client.Snapshot(); usage.Calls != 1 || usage.OutputTokens != 8 {
		t.Fatalf("billed thinking tokens were lost: %+v", usage)
	}
}

func TestClaudeSettingsEnvironmentAndLegacyCompatibility(t *testing.T) {
	testConfigDir(t)
	t.Setenv("TAILG_AI_PROVIDER", "claude")
	t.Setenv("TAILG_AI_API_KEY", "")
	t.Setenv("TAILG_AI_BASE_URL", "")
	t.Setenv("TAILG_AI_WORKSPACE_ID", "")
	t.Setenv("TAILG_AI_MODEL", "my-claude-model")
	t.Setenv("ANTHROPIC_API_KEY", "native-key")
	t.Setenv("ANTHROPIC_WORKSPACE_ID", "wrkspc_test")
	cfg, err := EnvConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != ProviderAnthropic || cfg.BaseURL != AnthropicBaseURL || cfg.APIKey != "native-key" || cfg.WorkspaceID != "wrkspc_test" {
		t.Fatalf("wrong defaults: %+v", cfg)
	}
	if err := SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILG_AI_PROVIDER", "openai")
	t.Setenv("TAILG_AI_API_KEY", "stale-key")
	restored, err := EnvConfig(nil)
	if err != nil || restored.Provider != ProviderAnthropic || restored.WorkspaceID != "wrkspc_test" || restored.APIKey != "native-key" {
		t.Fatalf("Claude settings lost: %+v %v", restored, err)
	}
	// Existing JSON without a provider remains a Chat Completions configuration.
	path, err := SettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"api_key":"legacy-key","model":"legacy-model","base_url":"https://api.openai.com/v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, _, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(legacy)
	if err != nil || !strings.HasSuffix(client.endpoint, "/chat/completions") {
		t.Fatalf("legacy configuration broken: %v", err)
	}
	if _, err := New(Config{Provider: "unsupported", APIKey: "key", Model: "model"}); err == nil {
		t.Fatal("unsupported provider accepted")
	}
}
