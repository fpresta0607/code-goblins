package main

import (
	"context"
	"os"

	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/openrouter"
)

// readKeySpend reads the spend Pi's OpenRouter key reports at the provider,
// from Pi's own configuration folder on this machine and from nowhere else:
// the folder Pi itself reads, PI_CODING_AGENT_DIR or ~/.pi/agent. A profile
// folder that cannot be read leaves the environment as the only source.
func readKeySpend(ctx context.Context, getenv func(string) string) openrouter.Reading {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return openrouter.Reader{Env: getenv}.Read(ctx)
	}
	pi, _ := harnessmap.Find(getenv, userHome).Harness("pi")
	return openrouter.Reader{Dir: pi.Path, Env: getenv}.Read(ctx)
}
