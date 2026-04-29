package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ---------------------------------------------------------------------------
// Ollama backend — plain HTTP to local Ollama server
// ---------------------------------------------------------------------------

type ollamaBackend struct {
	model   string
	baseURL string
	client  *http.Client
}

func newOllamaBackend(cfg Config, baseURL string) *ollamaBackend {
	model := cfg.Model
	if model == "" {
		model = "llama3"
	}
	client, _ := cfg.HTTPClient.(*http.Client)
	if client == nil {
		client = &http.Client{}
	}
	return &ollamaBackend{
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
	}
}

func (b *ollamaBackend) Call(ctx context.Context, prompt string) (string, error) {
	type reqBody struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		Stream bool   `json:"stream"`
	}
	type respBody struct {
		Response string `json:"response"`
		Error    string `json:"error,omitempty"`
	}

	body, err := json.Marshal(reqBody{
		Model:  b.model,
		Prompt: prompt,
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("ollama: marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ollama: reading response: %w", err)
	}

	var rb respBody
	if err := json.Unmarshal(raw, &rb); err != nil {
		return "", fmt.Errorf("ollama: unmarshalling response: %w", err)
	}
	if rb.Error != "" {
		return "", fmt.Errorf("ollama API error: %s", rb.Error)
	}
	return strings.TrimSpace(rb.Response), nil
}
