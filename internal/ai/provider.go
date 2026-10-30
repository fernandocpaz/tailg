package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	ProviderOpenAI     = "openai"
	ProviderAnthropic  = "anthropic"
	AnthropicBaseURL   = "https://api.anthropic.com/v1"
	DefaultClaudeModel = "claude-sonnet-5-5"
)

// An omitted provider preserves the original OpenAI-compatible configuration.
func providerDefaults(cfg *Config) error {
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch cfg.Provider {
	case "", ProviderOpenAI:
		cfg.Provider = ProviderOpenAI
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.openai.com/v1"
		}
	case "claude", ProviderAnthropic:
		cfg.Provider = ProviderAnthropic
		if cfg.BaseURL == "" {
			cfg.BaseURL = AnthropicBaseURL
		}
	default:
		return fmt.Errorf("unsupported AI provider %q; choose openai or anthropic", cfg.Provider)
	}
	return nil
}

// The two APIs use different authentication and system-prompt formats.
func (c *Client) authorize(request *http.Request) {
	request.Header.Set("Content-Type", "application/json")
	if c.cfg.Provider == ProviderAnthropic {
		request.Header.Set("x-api-key", c.cfg.APIKey)
		request.Header.Set("anthropic-version", "2026-10-21")
		if c.cfg.WorkspaceID != "" {
			request.Header.Set("anthropic-workspace-id", c.cfg.WorkspaceID)
		}
	} else {
		request.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
}

func (c *Client) requestBody(messages []chatMessage, maxTokens int) ([]byte, error) {
	if c.cfg.Provider == ProviderAnthropic {
		var system string
		var turns []chatMessage
		for _, message := range messages {
			if message.Role == "system" {
				system = message.Content
			} else {
				turns = append(turns, message)
			}
		}
		return json.Marshal(struct {
			Model     string        `json:"model"`
			System    string        `json:"system,omitempty"`
			Messages  []chatMessage `json:"messages"`
			MaxTokens int           `json:"max_tokens"`
		}{c.cfg.Model, system, turns, maxTokens})
	}
	return json.Marshal(struct {
		Model     string        `json:"model"`
		Messages  []chatMessage `json:"messages"`
		MaxTokens int           `json:"max_tokens"`
	}{c.cfg.Model, messages, maxTokens})
}

type completion struct {
	text                      string
	inputTokens, outputTokens int
}

func (c *Client) decodeCompletion(body []byte) (completion, error) {
	if c.cfg.Provider == ProviderAnthropic {
		var response struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Usage struct {
				InputTokens   int `json:"input_tokens"`
				OutputTokens  int `json:"output_tokens"`
				CacheCreation int `json:"cache_creation_input_tokens"`
				CacheRead     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(body, &response) != nil {
			return completion{}, errors.New("Claude returned an invalid Messages response")
		}
		var parts []string
		for _, block := range response.Content {
			if block.Type == "text" {
				parts = append(parts, block.Text)
			}
		}
		text := strings.Join(parts, "\n")
		// A short validation call can exhaust its token allowance on thinking.
		// A well-formed assistant message still proves credentials/model access.
		// Explain separately requires visible text for the inspector.
		if strings.TrimSpace(text) == "" && (response.Type != "message" || response.Role != "assistant" || len(response.Content) == 0) {
			return completion{}, errors.New("Claude returned no text in its Messages response")
		}
		return completion{text, response.Usage.InputTokens + response.Usage.CacheCreation + response.Usage.CacheRead, response.Usage.OutputTokens}, nil
	}
	var response chatResponse
	if json.Unmarshal(body, &response) != nil || len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return completion{}, errors.New("AI provider returned no compatible chat completion")
	}
	return completion{response.Choices[0].Message.Content, response.Usage.PromptTokens, response.Usage.CompletionTokens}, nil
}
