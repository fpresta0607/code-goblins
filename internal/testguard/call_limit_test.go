package testguard

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// slowGit holds each of its first slowCalls git calls for pause, as a loaded
// machine does, and fails one whose context ends during the hold. The hold is
// all the time a call spends against its limit: git itself then runs unbound,
// so a slow runner cannot make the limit the test means to keep run out.
type slowGit struct {
	pause     time.Duration
	slowCalls int
	calls     int
}

func (git *slowGit) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	git.calls++
	if git.calls <= git.slowCalls {
		select {
		case <-ctx.Done():
			return execx.Result{}, ctx.Err()
		case <-time.After(git.pause): // stall-accepted: the hold only slows a call, and a pause lengthens it no more than the call's own awake limit.
		}
	}
	return execx.OSRunner{}.Run(context.WithoutCancel(ctx), request)
}

// hungGit never answers its first git show and runs every other call.
type hungGit struct{}

func (hungGit) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if slices.Contains(request.Args, "show") {
		<-ctx.Done()
		return execx.Result{}, ctx.Err()
	}
	return execx.OSRunner{}.Run(ctx, request)
}

// Issue 1959: tests-kept gave its whole check one deadline, so on a loaded
// machine a branch with several gate commits failed it before every commit
// was read, though no single git call was stuck. The limit bounds one call:
// calls that together outlast it, each within it, finish the check.
func TestCheckFinishesWhenItsGitCallsTogetherOutlastOneCallsLimit(t *testing.T) {
	// Arrange
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	write("guard_test.go", "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n\nfunc TestGuardStands(t *testing.T) {}\n")
	git("commit", "-qam", "no-mistakes(test): add a guard")
	const limit = 5 * time.Second
	slow := &slowGit{pause: 3 * time.Second, slowCalls: 2}

	// Act
	result, err := Check(context.Background(), slow, dir, nil, limit)

	// Assert
	if err != nil {
		t.Fatalf("check failed after %d git calls: %v; want it to finish, since no one call outlasted its %s limit", slow.calls, err, limit)
	}
	if slow.calls <= slow.slowCalls || time.Duration(slow.slowCalls)*slow.pause <= limit {
		t.Fatalf("premise: %d calls held %s each must together outlast the %s limit", slow.calls, slow.pause, limit)
	}
	if result.Commits != 1 || len(result.Removals) != 0 {
		t.Fatalf("result = %+v, want the one gate commit read and nothing removed", result)
	}
}

// A git call that never returns still fails the check, naming that call
// and the limit it outlasted.
func TestCheckFailsOnAGitCallThatNeverReturnsAndNamesIt(t *testing.T) {
	// Arrange
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	write("guard_test.go", "package x\n")
	git("commit", "-qam", "no-mistakes(test): drop a failing guard")
	const limit = time.Second

	// Act
	_, err := Check(context.Background(), hungGit{}, dir, nil, limit)

	// Assert
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the hung call's limit to end the check", err)
	}
	for _, want := range []string{"git -c core.quotePath=false show", "no answer within 1s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}
}

// hungMergeBase never answers git merge-base HEAD origin/HEAD, runs every
// other call and records what each call asked.
type hungMergeBase struct {
	calls [][]string
}

func (git *hungMergeBase) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	git.calls = append(git.calls, request.Args)
	if slices.Equal(request.Args, []string{"merge-base", "HEAD", "origin/HEAD"}) {
		<-ctx.Done()
		return execx.Result{}, ctx.Err()
	}
	return execx.OSRunner{}.Run(ctx, request)
}

// A merge base call that never answers fails the check and is named, rather
// than the check moving on to origin/main, which may be another branch than
// the one origin/HEAD names and so read the wrong gate commits.
func TestCheckFailsOnAMergeBaseThatNeverReturnsAndNamesIt(t *testing.T) {
	// Arrange
	dir, git, write := scratchRepo(t, "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	write("guard_test.go", "package x\n\nfunc TestGuardHolds(t *testing.T) { t.Log(1) }\n")
	git("commit", "-qam", "no-mistakes(review): tighten the guard")
	hung := &hungMergeBase{}
	const limit = time.Second

	// Act
	_, err := Check(context.Background(), hung, dir, nil, limit)

	// Assert
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the hung merge base's limit to end the check", err)
	}
	for _, want := range []string{"git merge-base HEAD origin/HEAD", "no answer within 1s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}
	if len(hung.calls) != 1 {
		t.Errorf("calls = %q, want the check to end at the hung merge base", hung.calls)
	}
}
