package pipeline

import (
	"bytes"
	"context"
	"errors"
	"os"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// noPublishIntent keeps a run's intent out of the pull request it opens.
const noPublishIntent = "--no-publish-intent"

// Start is how one gate run is started with its intent.
type Start struct {
	// Args start the run: no-mistakes' axi run, the intent, and
	// --no-publish-intent. The caller adds its wait and launch selection
	// after them.
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
// Every run starts with --no-publish-intent: an intent is a task's private
// brief, with machine-local paths, ownership decisions and routing, and
// no-mistakes would otherwise publish it whole as the Intent section of the
// pull request it opens and of every later body refresh (#1866). The flag is
// kept on the run, and every step but the pull request's drafting still reads
// the whole intent. A no-mistakes before 1.80.0 has no such flag, so no run
// starts on it.
//
// Each issue in closes, which the run's pull request closes when it merges,
// goes to no-mistakes as --closes, the one place it writes a live closing
// reference from, since it neutralizes a closing keyword in every text an
// agent wrote, the intent included. A no-mistakes before 1.87.0 takes no
// --closes, so a start that names an issue on one starts no run.
func StartOf(ctx context.Context, commands execx.Runner, dir string, env []string, intent string, closes []string) (Start, error) {
	help, err := commands.Run(ctx, execx.Request{Dir: dir, Env: env, Name: "no-mistakes", Args: []string{"help", "axi", "run"}})
	if err != nil {
		return Start{}, err
	}
	if !bytes.Contains(help.Stdout, []byte(noPublishIntent)) {
		return Start{}, errors.New("this no-mistakes has no " + noPublishIntent + ", so it would publish the gate's intent, the task's private brief, in the pull request it opens; no run starts. Update no-mistakes to 1.80.0 or later")
	}
	if len(closes) > 0 && !bytes.Contains(help.Stdout, []byte("--closes")) {
		return Start{}, errors.New("this no-mistakes takes no --closes, so the pull request its gate opened would close no issue, and no run starts: update no-mistakes to 1.87.0 or later")
	}
	var closing []string
	for _, issue := range closes {
		closing = append(closing, "--closes", issue)
	}
	if !bytes.Contains(help.Stdout, []byte("--intent-file")) {
		return Start{Args: append([]string{"axi", "run", "--intent", intent, noPublishIntent}, closing...), IsOnCommandLine: true}, nil
	}
	file, err := os.CreateTemp("", "goblins-gate-intent-*.txt")
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
	return Start{Args: append([]string{"axi", "run", "--intent-file", file.Name(), noPublishIntent}, closing...), file: file.Name()}, nil
}
