package classifier

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// codexBackend invokes the `codex` CLI with the prompt as a positional arg.
type codexBackend struct {
	model string // stored for forward compatibility
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
