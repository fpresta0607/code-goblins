package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const (
	recoveryRun       = "01M2DCF5MS8AV0CCTJ4ADN8MM9"
	recoveryRepo      = "3946e592fa2c"
	recoveryBranch    = "feat/pd-fly-rightsize"
	recoveryRecorded  = "b6aacde207d44bbd1533999890d94d6a5b477c45"
	recoveryPreserved = "38ac7ca90f1754a4bb4b1175e143d949f3dc6183"
)

type recoveryRunner struct {
	patchesEqual bool
	alreadyOwned bool
	localHead    string
	syncChecks   int
	requests     []execx.Request
}

func (r *recoveryRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	switch request.Name {
	case "sqlite3":
		sql := request.Args[len(request.Args)-1]
		if strings.Contains(sql, "SELECT changes()") {
			return execx.Result{Stdout: []byte(`[{"n":1}]`)}, nil
		}
		return execx.Result{Stdout: []byte(`[{
			"run_id":"` + recoveryRun + `",
			"repo_id":"` + recoveryRepo + `",
			"branch":"` + recoveryBranch + `",
			"status":"failed",
			"recorded_head":"` + recoveryRecorded + `",
			"submitted_head":"` + recoveryPreserved + `",
			"pushed_head":"",
			"custody_returned_at":0
		}]`)}, nil
	case "git":
		joined := strings.Join(request.Args, " ")
		switch {
		case joined == "status --porcelain --untracked-files=all":
			return execx.Result{}, nil
		case joined == "rev-parse HEAD":
			head := r.localHead
			if head == "" {
				head = recoveryPreserved
			}
			return execx.Result{Stdout: []byte(head + "\n")}, nil
		case joined == "rev-parse refs/heads/"+recoveryBranch:
			return execx.Result{Stdout: []byte(recoveryPreserved + "\n")}, nil
		case strings.HasPrefix(joined, "cat-file -e "):
			return execx.Result{}, nil
		case joined == "merge-base --is-ancestor "+recoveryRecorded+" "+recoveryPreserved:
			return execx.Result{ExitCode: 1}, nil
		case joined == "rev-list --parents -n 1 "+recoveryRecorded:
			return execx.Result{Stdout: []byte(recoveryRecorded + " old-parent\n")}, nil
		case joined == "rev-list --parents -n 1 "+recoveryPreserved:
			return execx.Result{Stdout: []byte(recoveryPreserved + " new-parent\n")}, nil
		case joined == "diff --binary --full-index --no-ext-diff --no-renames old-parent "+recoveryRecorded:
			return execx.Result{Stdout: []byte("same patch")}, nil
		case joined == "diff --binary --full-index --no-ext-diff --no-renames new-parent "+recoveryPreserved:
			if r.patchesEqual {
				return execx.Result{Stdout: []byte("same patch")}, nil
			}
			return execx.Result{Stdout: []byte("different patch")}, nil
		case strings.HasPrefix(joined, "rev-parse --verify --quiet refs/no-mistakes/recovery/"):
			return execx.Result{ExitCode: 1}, nil
		case strings.HasPrefix(joined, "update-ref refs/no-mistakes/recovery/"):
			return execx.Result{}, nil
		}
	case "no-mistakes":
		if strings.Join(request.Args, " ") == "axi sync --check" {
			r.syncChecks++
			if !r.alreadyOwned && r.syncChecks == 1 {
				return execx.Result{ExitCode: 1}, nil
			}
			head := r.localHead
			if head == "" {
				head = recoveryPreserved
			}
			return execx.Result{Stdout: []byte("branch_sync:\n  state: user_owned\n  safety: user_owned\n  local:\n    head: " + head + "\n    clean: true\n  pipeline:\n    run: \"" + recoveryRun + "\"\n    submitted_head: " + recoveryPreserved + "\n    current_head: " + recoveryPreserved + "\n")}, nil
		}
		return execx.Result{Stdout: []byte("custody returned\n")}, nil
	}
	return execx.Result{}, errors.New("unexpected command: " + request.Name + " " + strings.Join(request.Args, " "))
}

func TestRecoverKeepLocalRepairsExactRebaseGateShapeWithoutReset(t *testing.T) {
	runner := &recoveryRunner{patchesEqual: true}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	result, err := reader.RecoverKeepLocal(context.Background(), `C:\dev\code-goblins\projects\precisiondocs`, `C:\dev\code-goblins\projects\precisiondocs\.worktrees\gb-pd-fly-rightsize`, recoveryBranch, []string{"NM_HOME=test"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != recoveryRun || result.Head != recoveryPreserved || !strings.Contains(string(result.NativeOutput), "custody returned") {
		t.Fatalf("result: %+v", result)
	}

	var anchored, repaired, recovered bool
	for _, request := range runner.requests {
		joined := request.Name + " " + strings.Join(request.Args, " ")
		if strings.Contains(joined, " reset ") || strings.HasSuffix(joined, " reset") {
			t.Fatalf("destructive reset invoked: %s", joined)
		}
		if strings.HasPrefix(joined, "git update-ref refs/no-mistakes/recovery/"+recoveryRun+"/recorded "+recoveryRecorded) {
			anchored = true
		}
		if request.Name == "sqlite3" && strings.Contains(joined, "UPDATE runs SET head_sha=") {
			if !anchored {
				t.Fatal("metadata repaired before stale commit was anchored")
			}
			repaired = true
		}
		if joined == "no-mistakes axi sync --recover --keep-local" {
			if !repaired {
				t.Fatal("native recovery ran before metadata repair")
			}
			recovered = true
		}
	}
	if !anchored || !repaired || !recovered {
		t.Fatalf("anchor=%v repair=%v recover=%v", anchored, repaired, recovered)
	}
}

func TestRecoverKeepLocalRefusesNonEquivalentDivergenceBeforeMutation(t *testing.T) {
	runner := &recoveryRunner{}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", recoveryBranch, nil); err == nil {
		t.Fatal("non-equivalent divergence accepted")
	}
	for _, request := range runner.requests {
		joined := request.Name + " " + strings.Join(request.Args, " ")
		if joined == "no-mistakes axi sync --recover --keep-local" || strings.Contains(joined, "update-ref") || strings.Contains(joined, "UPDATE runs") {
			t.Fatalf("unsafe recovery mutated state: %s", joined)
		}
	}
}

func TestRecoverKeepLocalIsIdempotentAfterUserOwnedBranchAdvances(t *testing.T) {
	const advanced = "58b90613458c083ce27a52d50956b9695adcd828"
	runner := &recoveryRunner{alreadyOwned: true, localHead: advanced}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", recoveryBranch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Head != advanced {
		t.Fatalf("head=%s, want %s", result.Head, advanced)
	}
	for _, request := range runner.requests {
		joined := request.Name + " " + strings.Join(request.Args, " ")
		if joined == "no-mistakes axi sync --recover --keep-local" || strings.Contains(joined, "update-ref") || strings.Contains(joined, "UPDATE runs") {
			t.Fatalf("idempotent recovery mutated state: %s", joined)
		}
	}
}
