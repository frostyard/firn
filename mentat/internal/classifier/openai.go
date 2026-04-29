package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// OpenAI backend — plain HTTP, no SDK dependency
// ---------------------------------------------------------------------------

type openaiBackend struct {
	model  string
	apiKey string
	client *http.Client
}

func newOpenAIBackend(cfg Config) *openaiBackend {
	model := cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}
	client, _ := cfg.HTTPClient.(*http.Client)
	if client == nil {
		client = &http.Client{}
	}
	return &openaiBackend{
		model:  model,
		apiKey: os.Getenv("OPENAI_API_KEY"),
		client: client,
	}
}

func (b *openaiBackend) Call(ctx context.Context, prompt string) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type reqBody struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
	}
	type choice struct {
		Message message `json:"message"`
	}
	type respBody struct {
		Choices []choice `json:"choices"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	body, err := json.Marshal(reqBody{
		Model:    b.model,
		Messages: []message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", fmt.Errorf("openai: marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.apiKey)

	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("openai: reading response: %w", err)
	}

	var rb respBody
	if err := json.Unmarshal(raw, &rb); err != nil {
		return "", fmt.Errorf("openai: unmarshalling response: %w", err)
	}
	if rb.Error != nil {
		return "", fmt.Errorf("openai API error: %s", rb.Error.Message)
	}
	if len(rb.Choices) == 0 {
		return "", fmt.Errorf("openai: empty choices in response")
	}
	return strings.TrimSpace(rb.Choices[0].Message.Content), nil
}
