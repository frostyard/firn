package specgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// jsonDecode decodes JSON from an io.Reader into v. Used by specgen.go via
// jsonUnmarshal to keep encoding/json in a single file.
func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// newLLMCaller constructs the appropriate LLMCaller from cfg.
func newLLMCaller(cfg Config) (LLMCaller, error) {
	backend := cfg.Backend
	if backend == "" {
		// Auto-detect from environment.
		dc := DefaultConfig()
		backend = dc.Backend
		if cfg.OllamaBaseURL == "" {
			cfg.OllamaBaseURL = dc.OllamaBaseURL
		}
	}

	switch backend {
	case "claude":
		return &claudeBackend{model: cfg.Model}, nil
	case "copilot":
		return &copilotBackend{model: cfg.Model}, nil
	case "codex":
		return &codexBackend{model: cfg.Model}, nil
	case "openai":
		return newOpenAIBackend(cfg), nil
	case "ollama":
		base := cfg.OllamaBaseURL
		if base == "" {
			base = "http://localhost:11434"
		}
		return newOllamaBackend(cfg, base), nil
	case "":
		return nil, ErrNoBackend
	default:
		return nil, fmt.Errorf("unknown LLM backend %q: use claude, copilot, codex, openai, or ollama", backend)
	}
}

// ---------------------------------------------------------------------------
// Claude backend — invokes the `claude` CLI
// ---------------------------------------------------------------------------

type claudeBackend struct {
	model string
}

func (b *claudeBackend) Call(ctx context.Context, prompt string) (string, error) {
	args := []string{"--print"}
	if b.model != "" {
		args = append(args, "--model", b.model)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, "claude", args...)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// ---------------------------------------------------------------------------
// Copilot backend — invokes the `pi` CLI
//
// Flags used:
//
//	--print            non-interactive mode: process prompt and exit
//	--no-session       ephemeral — do not save session state
//	--no-context-files skip AGENTS.md / CLAUDE.md discovery
//	--no-tools         pure text generation, no filesystem/bash tools
//
// The prompt is passed via stdin so that arbitrarily long prompts are handled
// safely without hitting OS argument-length limits.
// ---------------------------------------------------------------------------

type copilotBackend struct {
	model string
}

func (b *copilotBackend) Call(ctx context.Context, prompt string) (string, error) {
	args := []string{"--available-tools=", "--prompt", prompt}
	if b.model != "" {
		args = append(args, "--model", b.model)
	}

	cmd := exec.CommandContext(ctx, "copilot", args...)
	cmd.Stdin = nil

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("copilot CLI: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

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

// codexBackend invokes the `codex` CLI with the prompt as a positional arg.
type codexBackend struct {
	model string
}

func (b *codexBackend) Call(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "codex", prompt)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("codex CLI: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
