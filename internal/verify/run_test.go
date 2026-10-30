package verify

import (
	"context"
	"errors"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/project"
	"testing"
	"time"
)

type fake struct {
	codes []int
	i     int
}

func (f *fake) Run(context.Context, execx.Request) (execx.Result, error) {
	c := 0
	if f.i < len(f.codes) {
		c = f.codes[f.i]
	}
	f.i++
	return execx.Result{ExitCode: c}, nil
}
func TestFastAndFull(t *testing.T) {
	for _, scope := range []string{"fast", "full"} {
		f := &fake{}
		r := Runner{Commands: f}
		got, e := r.Run(context.Background(), []project.Command{{"go", "test", "./..."}}, scope, "t", "sha", "", time.Second)
		if e != nil || len(got) != 1 || got[0].Scope != scope {
			t.Fatalf("%s %#v %v", scope, got, e)
		}
	}
}
func TestFailureEvidence(t *testing.T) {
	r := Runner{Commands: &fake{codes: []int{1}}}
	got, e := r.Run(context.Background(), []project.Command{{"x"}}, "fast", "t", "sha", "", time.Second)
	if e == nil || got[0].ExitCode != 1 {
		t.Fatalf("%#v %v", got, e)
	}
}

type blockingFake struct{}

func (blockingFake) Run(ctx context.Context, _ execx.Request) (execx.Result, error) {
	<-ctx.Done()
	return execx.Result{}, ctx.Err()
}

func TestTimeoutEvidence(t *testing.T) {
	r := Runner{Commands: blockingFake{}}
	got, err := r.Run(context.Background(), []project.Command{{"slow"}}, "fast", "t", "sha", "", 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if len(got) != 1 || !got[0].TimedOut || got[0].ExitCode != -1 {
		t.Fatalf("unexpected timeout evidence: %#v", got)
	}
}

// exitingFake is a run that ends by itself with its exit code.
type exitingFake struct{ code int }

func (fake exitingFake) Run(context.Context, execx.Request) (execx.Result, error) {
	return execx.Result{ExitCode: fake.code}, nil
}

// A command that exits 125 tested nothing, so the tier is no pass and its
// result does not read as a test that failed. Any other exit does.
func TestRunTellsACommandThatGaveNoResultFromOneThatFailed(t *testing.T) {
	for _, test := range []struct {
		code int
		want string
	}{{125, "not_run"}, {1, "failed"}, {2, "failed"}} {
		t.Run(test.want, func(t *testing.T) {
			// Arrange
			r := Runner{Commands: exitingFake{code: test.code}}

			// Act
			got, err := r.Run(context.Background(), []project.Command{{"cfo", "gate", "test"}, {"never"}}, "fast", "t", "sha", "", 0)

			// Assert
			if err == nil {
				t.Fatal("the tier passed, want an error")
			}
			if len(got) != 1 || got[0].Status != test.want || got[0].ExitCode != test.code {
				t.Fatalf("results %#v, want one with status %s and exit %d", got, test.want, test.code)
			}
		})
	}
}

// limitedRunner is a measured runner whose every run fails with failure.
type limitedRunner struct{ failure error }

func (runner limitedRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	result, _, err := runner.RunWithin(ctx, request, 0)
	return result, err
}

func (runner limitedRunner) RunWithin(context.Context, execx.Request, time.Duration) (execx.Result, string, error) {
	return execx.Result{ExitCode: -1}, "record.json", runner.failure
}

func TestRunnerCallsOnlyTheCommandsLimitATimeout(t *testing.T) {
	for _, test := range []struct {
		name       string
		failure    error
		isTimedOut bool
	}{
		{name: "the command's limit", failure: errors.Join(context.Canceled, ErrLimit), isTimedOut: true},
		{name: "a wait for custody", failure: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runner := Runner{Commands: limitedRunner{failure: test.failure}}

			// Act
			results, err := runner.Run(context.Background(), []project.Command{{"go", "test"}}, "fast", "t", "", "", time.Minute)

			// Assert
			if err == nil || len(results) != 1 || results[0].TimedOut != test.isTimedOut || results[0].Execution != "record.json" {
				t.Fatalf("results = %+v, %v, want timed out %v", results, err, test.isTimedOut)
			}
		})
	}
}
