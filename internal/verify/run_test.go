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
