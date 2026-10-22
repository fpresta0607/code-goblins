package watch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const (
	// ciRunnerAskLimit bounds asking the desktop CI runner whether a job
	// waits: the service's own GitHub calls take up to 30 seconds.
	ciRunnerAskLimit = 35 * time.Second
	// ciRunnerStartLimit bounds starting a job's detached turn, which
	// returns once the run is started.
	ciRunnerStartLimit = 30 * time.Second
	// ciRunnerSayEvery is how often the same trouble is said in the watch's
	// log, rather than at every look.
	ciRunnerSayEvery = time.Hour
	// ciRunnerJobMemory is the most memory a job's turn is expected to use,
	// which the line weighs before it starts the turn beside another: the
	// VM's 6 GB at most, its Hyper-V worker, and the job's own process. The
	// VM's memory is the worker's, outside the turn's own processes, so the
	// line could not see it otherwise.
	ciRunnerJobMemory = "7GiB"
)

// ciRunnerWatch starts each desktop CI job that waits as a turn of its own in
// the machine's heavy line, at the lowest priority, one at a time: no job
// starts while the run started for the last one still lives.
type ciRunnerWatch struct {
	// ask asks the desktop CI runner's service.
	ask func(ctx context.Context, request cirunner.Request, reply func(cirunner.Reply)) error
	// commands runs this build's cfo gate turn, which detaches the job's run.
	commands execx.Runner
	// started is when process pid started, or false once it has ended.
	started func(pid int) (time.Time, bool)
	// executable is this build.
	executable string
	// logs is the folder each job's run logs to.
	logs string
	now  func() time.Time
	// say adds a line to the watch's log.
	say func(line string)

	pid   int
	start time.Time
	said  map[string]time.Time
}

// look starts a turn for the desktop CI job that waits, unless the run
// started for the last one still lives or no service answers.
func (w *ciRunnerWatch) look(ctx context.Context) {
	if w.pid != 0 {
		if start, isRunning := w.started(w.pid); isRunning && start.Equal(w.start) {
			return
		}
		w.pid = 0
	}
	asking, cancel := context.WithTimeout(ctx, ciRunnerAskLimit)
	defer cancel()
	var pending cirunner.Pending
	err := w.ask(asking, cirunner.Request{Kind: cirunner.AskPending}, func(reply cirunner.Reply) {
		if reply.Pending != nil {
			pending = *reply.Pending
		}
	})
	switch {
	case errors.Is(err, cirunner.ErrNotServing):
		return
	case err != nil:
		w.sayOnce("ask", "the desktop CI runner did not say whether a job waits: "+err.Error())
		return
	case !pending.IsWaiting:
		return
	}
	log := filepath.Join(w.logs, fmt.Sprintf("job-%d-%s.log", pending.RunID, w.now().UTC().Format("20060102-150405")))
	starting, cancelStart := context.WithTimeout(ctx, ciRunnerStartLimit)
	defer cancelStart()
	result, err := w.commands.Run(starting, execx.Request{Name: w.executable, Args: []string{
		"gate", "turn", "--low-priority", "--memory", ciRunnerJobMemory,
		"--as", fmt.Sprintf("desktop CI: %s of run %d", pending.Job, pending.RunID),
		"--detach", log,
		"--", w.executable, "ci-runner", "job",
	}})
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	if err != nil {
		w.sayOnce("start", fmt.Sprintf("the turn for desktop CI job %q of run %d did not start: %v", pending.Job, pending.RunID, err))
		return
	}
	data, err := fsx.ReadFile(log + ".pid")
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || parseErr != nil {
		w.say(fmt.Sprintf("started a turn for desktop CI job %q of run %d, but its pid cannot be read: %v", pending.Job, pending.RunID, errors.Join(err, parseErr)))
		return
	}
	w.say(fmt.Sprintf("started a turn for desktop CI job %q of run %d as pid %d, logging to %s", pending.Job, pending.RunID, pid, log))
	if start, isRunning := w.started(pid); isRunning {
		w.pid, w.start = pid, start
	}
}

// sayOnce says line under key at most once each ciRunnerSayEvery.
func (w *ciRunnerWatch) sayOnce(key, line string) {
	if last, isSaid := w.said[key]; isSaid && w.now().Sub(last) < ciRunnerSayEvery {
		return
	}
	if w.said == nil {
		w.said = map[string]time.Time{}
	}
	w.said[key] = w.now()
	w.say(line)
}

// watch looks for a desktop CI job each every, until ctx ends.
func (w *ciRunnerWatch) watch(ctx context.Context, sleep func(context.Context, time.Duration), every time.Duration) {
	for ctx.Err() == nil {
		w.look(ctx)
		sleep(ctx, every)
	}
}

// CIRunnerFolder is where the watch logs, and each job's run logs, under the
// home's state, and where cfo ci-runner parity keeps its records.
func CIRunnerFolder(stateDir string) string {
	return filepath.Join(stateDir, "ci-runner")
}

// appendLine is a say that adds each line, with when it was said, to the
// file at path.
func appendLine(path string, now func() time.Time) func(string) {
	return func(line string) {
		file, err := fsx.OpenAppend(path, 0o600)
		if err != nil {
			return
		}
		defer file.Close()
		fmt.Fprintf(file, "%s %s\n", now().UTC().Format(time.RFC3339), line)
	}
}
