// Package ai is a minimal client for OpenAI-compatible chat-completion APIs
// (used here with Alibaba Cloud DashScope / Model Studio running Qwen models).
// It deliberately uses only net/http so no new dependencies are introduced.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config carries the endpoint settings resolved from environment/config.
type Config struct {
	APIKey  string
	BaseURL string // e.g. https://dashscope-intl.aliyuncs.com/compatible-mode/v1
	Model   string // e.g. qwen-flash
}

// Client talks to the chat-completions endpoint. A zero/nil client or an empty
// API key means the feature is disabled and callers should skip analysis.
type Client struct {
	cfg Config
	hc  *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		// LLM completions can take a while; 90s upper bound keeps the manual
		// "re-analyze" HTTP request from hanging forever.
		hc: &http.Client{Timeout: 90 * time.Second},
	}
}

// Enabled reports whether an API key is configured.
func (c *Client) Enabled() bool {
	return c != nil && strings.TrimSpace(c.cfg.APIKey) != ""
}

// Model returns the configured model name (for display in the UI).
func (c *Client) Model() string { return c.cfg.Model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// Chat sends a system+user prompt pair and returns the assistant text.
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("ai: no API key configured")
	}
	body, err := json.Marshal(chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0.3,
	})
	if err != nil {
		return "", fmt.Errorf("ai: marshal request: %w", err)
	}

	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai: call failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("ai: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ai: api error (http %d): %s", resp.StatusCode, snippet(string(raw)))
	}

	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("ai: decode response: %w", err)
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("ai: api error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("ai: empty completion")
	}
	return strings.TrimSpace(decoded.Choices[0].Message.Content), nil
}

// snippet trims raw error bodies so logs/exceptions stay readable.
func snippet(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
