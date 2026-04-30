package classifier

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ---------------------------------------------------------------------------
// Copilot backend — invokes the `copilot` CLI
//
// Flags used:
//   --print            non-interactive mode: process prompt and exit
//   --no-session       ephemeral — do not save session state
//   --no-context-files skip AGENTS.md / CLAUDE.md discovery
//   --no-tools         pure text generation, no filesystem/bash tools
//
// The prompt is passed via stdin so that arbitrarily long prompts are handled
// safely without hitting OS argument-length limits.
//
// An optional model override is applied via --model when Config.Model is set.
// ---------------------------------------------------------------------------

type copilotBackend struct {
	model string
}

func (b *copilotBackend) Call(ctx context.Context, prompt string) (string, error) {
	args := []string{"--prompt", prompt}
	if b.model != "" {
		args = append(args, "--model", b.model)
	}

	cmd := exec.CommandContext(ctx, "copilot", args...)
	cmd.Stdin = nil // prompt passed as flag, not stdin

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("copilot CLI: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
