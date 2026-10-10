package pipeline

import (
	"bytes"
	"context"
	"os"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Start is how one gate run is started with its intent.
type Start struct {
	// Args start the run: no-mistakes' axi run and the intent. The caller
	// adds its wait and launch selection after them.
	Args []string
	// IsOnCommandLine reports that Args hold the intent itself, which only a
	// no-mistakes without --intent-file needs.
	IsOnCommandLine bool
	file            string
}

// Close removes the file the intent was handed over in, once the run has
// been started.
func (start Start) Close() {
	if start.file != "" {
		_ = os.Remove(start.file)
	}
}

// StartOf says how to start a gate run of intent with the no-mistakes that
// commands runs in dir under env. The intent goes to no-mistakes in a file,
// never on its command line: an intent is prose, often several thousand
// characters that name a shell, and such a command line is what Microsoft
// Defender detects as a lure pasted into a terminal
// (Trojan:Win32/ClickFix.DQ!MTB on 2026-10-02) and what Windows then refuses
// to start. A no-mistakes before 1.86.0 has no --intent-file, and would take
// --intent - as an intent of one dash, so for such a build alone the intent
// stays on the command line, and the caller says so.
func StartOf(ctx context.Context, commands execx.Runner, dir string, env []string, intent string) (Start, error) {
	help, err := commands.Run(ctx, execx.Request{Dir: dir, Env: env, Name: "no-mistakes", Args: []string{"help", "axi", "run"}})
	if err != nil {
		return Start{}, err
	}
	if !bytes.Contains(help.Stdout, []byte("--intent-file")) {
		return Start{Args: []string{"axi", "run", "--intent", intent}, IsOnCommandLine: true}, nil
	}
	file, err := os.CreateTemp("", "cfo-gate-intent-*.txt")
	if err != nil {
		return Start{}, err
	}
	if _, err := file.WriteString(intent); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return Start{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return Start{}, err
	}
	return Start{Args: []string{"axi", "run", "--intent-file", file.Name()}, file: file.Name()}, nil
}
