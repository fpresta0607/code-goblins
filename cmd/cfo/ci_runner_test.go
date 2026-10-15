package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
)

// ciRunnerAnswering is a runtime whose desktop CI runner answers each request
// with replies, or with err.
func ciRunnerAnswering(t *testing.T, replies []cirunner.Reply, err error) (commandRuntime, *[]string) {
	t.Helper()
	runtime := defaultCommandRuntime()
	var asked []string
	runtime.askCIRunner = func(ctx context.Context, request cirunner.Request, reply func(cirunner.Reply)) error {
		asked = append(asked, request.Kind)
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			t.Errorf("asked %q with no deadline; want every ask bounded", request.Kind)
		}
		if err != nil {
			return err
		}
		for _, answer := range replies {
			reply(answer)
		}
		return nil
	}
	return runtime, &asked
}

// A job's exit says how it ended: 0 when it ran, 3 when no job came for the
// runner, 1 otherwise, its progress on standard error as it comes.
func TestCIRunnerJobExitsWithTheServicesOutcome(t *testing.T) {
	progress := cirunner.Reply{Kind: cirunner.ReplyProgress, Line: "the runner took a job"}
	for name, row := range map[string]struct {
		replies    []cirunner.Reply
		err        error
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		"ran": {
			replies:    []cirunner.Reply{progress, {Kind: cirunner.ReplyDone, Outcome: cirunner.Ran}},
			wantStdout: "cfo ci-runner: the job ended: ran\n",
			wantStderr: "cfo ci-runner: the runner took a job\n",
		},
		"no job came": {
			replies:    []cirunner.Reply{{Kind: cirunner.ReplyDone, Outcome: cirunner.NoJob}},
			wantExit:   exitNoCIJob,
			wantStdout: "cfo ci-runner: the job ended: no job\n",
		},
		"timed out": {
			replies:    []cirunner.Reply{progress, {Kind: cirunner.ReplyDone, Outcome: cirunner.TimedOut}},
			wantExit:   1,
			wantStdout: "cfo ci-runner: the job ended: timed out\n",
			wantStderr: "cfo ci-runner: the runner took a job\n",
		},
		"failed": {
			replies:    []cirunner.Reply{{Kind: cirunner.ReplyDone, Outcome: cirunner.Failed, Error: "cirunner: start the VM: exit 1"}},
			wantExit:   1,
			wantStdout: "cfo ci-runner: the job ended: failed\n",
			wantStderr: "cfo ci-runner: cirunner: start the VM: exit 1\n",
		},
		"no service runs": {
			err:        cirunner.ErrNotServing,
			wantExit:   1,
			wantStderr: "cfo ci-runner: cirunner: the CI runner service is not running\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, asked := ciRunnerAnswering(t, row.replies, row.err)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runCIRunner([]string{"job"}, &stdout, &stderr, runtime)

			// Assert
			if exit != row.wantExit || stdout.String() != row.wantStdout || stderr.String() != row.wantStderr {
				t.Errorf("exit %d, stdout %q, stderr %q; want %d, %q, %q", exit, stdout.String(), stderr.String(), row.wantExit, row.wantStdout, row.wantStderr)
			}
			if len(*asked) != 1 || (*asked)[0] != cirunner.AskRun {
				t.Errorf("asked %q; want one run", *asked)
			}
		})
	}
}

// Status says what the service says runs, and why it cannot when no service
// answers.
func TestCIRunnerStatusSaysWhatRuns(t *testing.T) {
	for name, row := range map[string]struct {
		err        error
		wantExit   int
		wantStdout string
	}{
		"a service answers": {wantStdout: "cfo ci-runner: no job has run since the service started\n"},
		"no service runs":   {err: cirunner.ErrNotServing, wantExit: 1},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, asked := ciRunnerAnswering(t, []cirunner.Reply{{Kind: cirunner.ReplyStatus, Line: "no job has run since the service started"}}, row.err)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runCIRunner([]string{"status"}, &stdout, &stderr, runtime)

			// Assert
			if exit != row.wantExit || stdout.String() != row.wantStdout || len(*asked) != 1 || (*asked)[0] != cirunner.AskStatus {
				t.Errorf("exit %d, stdout %q, stderr %q, asked %q; want %d and %q", exit, stdout.String(), stderr.String(), *asked, row.wantExit, row.wantStdout)
			}
		})
	}
}

// Anything but job or status is its usage, and asks nothing.
func TestCIRunnerRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, args := range [][]string{nil, {"run"}, {"job", "now"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Arrange
			runtime, asked := ciRunnerAnswering(t, nil, nil)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runCIRunner(args, &stdout, &stderr, runtime)

			// Assert
			if exit != 2 || stderr.String() != ciRunnerUsage+"\n" || len(*asked) != 0 {
				t.Errorf("exit %d, stderr %q, asked %q; want 2, the usage and nothing asked", exit, stderr.String(), *asked)
			}
		})
	}
}
