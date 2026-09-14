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
	scenario        recoveryScenario
	patchesEqual    bool
	alreadyOwned    bool
	localHead       string
	syncChecks      int
	gateAligned     bool
	databaseAligned bool
	failDatabase    bool
	anchorExists    bool
	requests        []execx.Request
}

type recoveryScenario struct {
	run, repo, branch, recorded, submitted string
}

func (r *recoveryRunner) values() recoveryScenario {
	if r.scenario.run != "" {
		return r.scenario
	}
	return recoveryScenario{run: recoveryRun, repo: recoveryRepo, branch: recoveryBranch, recorded: recoveryRecorded, submitted: recoveryPreserved}
}

func (r *recoveryRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	scenario := r.values()
	switch request.Name {
	case "sqlite3":
		sql := request.Args[len(request.Args)-1]
		if strings.Contains(sql, "SELECT changes()") {
			if r.localHead != "" && strings.Contains(sql, "head_sha='"+r.localHead+"'") && strings.Contains(sql, "submitted_head_sha='"+r.localHead+"'") {
				if r.failDatabase {
					return execx.Result{Stdout: []byte(`[{"n":0}]`)}, nil
				}
				r.databaseAligned = true
			}
			return execx.Result{Stdout: []byte(`[{"n":1}]`)}, nil
		}
		return execx.Result{Stdout: []byte(`[{
			"run_id":"` + scenario.run + `",
			"repo_id":"` + scenario.repo + `",
			"branch":"` + scenario.branch + `",
			"status":"failed",
			"recorded_head":"` + scenario.recorded + `",
			"submitted_head":"` + scenario.submitted + `",
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
				head = scenario.submitted
			}
			return execx.Result{Stdout: []byte(head + "\n")}, nil
		case joined == "rev-parse refs/heads/"+scenario.branch && r.gateAligned:
			return execx.Result{Stdout: []byte(r.localHead + "\n")}, nil
		case joined == "rev-parse refs/heads/"+scenario.branch:
			return execx.Result{Stdout: []byte(scenario.submitted + "\n")}, nil
		case strings.HasPrefix(joined, "rev-parse refs/no-mistakes/recovery/") && r.anchorExists:
			return execx.Result{Stdout: []byte(scenario.submitted + "\n")}, nil
		case strings.HasPrefix(joined, "rev-parse refs/no-mistakes/recovery/"):
			return execx.Result{ExitCode: 1}, nil
		case strings.HasPrefix(joined, "cat-file -e "):
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "merge-base --is-ancestor "):
			return execx.Result{ExitCode: 1}, nil
		case strings.HasPrefix(joined, "rev-list --parents -n 1 "):
			head := request.Args[len(request.Args)-1]
			parent := "new-parent"
			if head == scenario.recorded {
				parent = "old-parent"
			}
			return execx.Result{Stdout: []byte(head + " " + parent + "\n")}, nil
		case strings.HasPrefix(joined, "diff --binary --full-index --no-ext-diff --no-renames old-parent "):
			return execx.Result{Stdout: []byte("same patch")}, nil
		case strings.HasPrefix(joined, "diff --binary --full-index --no-ext-diff --no-renames new-parent "):
			if r.patchesEqual {
				return execx.Result{Stdout: []byte("same patch")}, nil
			}
			return execx.Result{Stdout: []byte("different patch")}, nil
		case joined == "patch-id --stable":
			if string(request.Stdin) == "same patch" {
				return execx.Result{Stdout: []byte("same-id commit\n")}, nil
			}
			return execx.Result{Stdout: []byte("different-id commit\n")}, nil
		case strings.HasPrefix(joined, "rev-parse --verify --quiet refs/no-mistakes/recovery/") && r.anchorExists:
			return execx.Result{Stdout: []byte(scenario.submitted + "\n")}, nil
		case strings.HasPrefix(joined, "rev-parse --verify --quiet refs/no-mistakes/recovery/"):
			return execx.Result{ExitCode: 1}, nil
		case strings.HasPrefix(joined, "fetch --no-tags --no-write-fetch-head "):
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "update-ref refs/heads/"+scenario.branch+" "+r.localHead+" "+scenario.submitted):
			r.gateAligned = true
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "update-ref refs/heads/"+scenario.branch+" "+scenario.submitted+" "+r.localHead):
			r.gateAligned = false
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "update-ref refs/no-mistakes/recovery/"):
			r.anchorExists = true
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
				head = scenario.submitted
			}
			pipelineHead := scenario.submitted
			currentHead := scenario.submitted
			if r.gateAligned {
				currentHead = head
			}
			if r.databaseAligned {
				pipelineHead = head
				currentHead = head
			}
			return execx.Result{Stdout: []byte("branch_sync:\n  state: user_owned\n  safety: user_owned\n  local:\n    head: " + head + "\n    clean: true\n  pipeline:\n    run: \"" + scenario.run + "\"\n    submitted_head: " + pipelineHead + "\n    current_head: " + currentHead + "\n")}, nil
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

func TestRecoverKeepLocalAlignsV172UserOwnedGateBeforeSuccess(t *testing.T) {
	const (
		run      = "01M2C018A26RY2Y5GFRKC5NXCS"
		branch   = "copy/hero-request-a-service"
		oldGate  = "37c93f2081cead9d12bab41d21d4a09d652ffabd"
		advanced = "18704b1fc56bf84c1183ee004d6f371447bc93a6"
	)
	runner := &recoveryRunner{
		scenario:     recoveryScenario{run: run, repo: recoveryRepo, branch: branch, recorded: oldGate, submitted: oldGate},
		patchesEqual: true,
		alreadyOwned: true,
		localHead:    advanced,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Head != advanced {
		t.Fatalf("head=%s, want %s", result.Head, advanced)
	}
	var fetched, gateAligned, databaseAligned bool
	for _, request := range runner.requests {
		joined := request.Name + " " + strings.Join(request.Args, " ")
		if joined == "no-mistakes axi sync --recover --keep-local" || strings.Contains(joined, " reset ") {
			t.Fatalf("unsafe recovery command: %s", joined)
		}
		if strings.HasPrefix(joined, "git fetch --no-tags --no-write-fetch-head worktree "+advanced) {
			fetched = true
		}
		if strings.HasPrefix(joined, "git update-ref refs/heads/"+branch+" "+advanced+" "+oldGate) {
			gateAligned = true
		}
		if request.Name == "sqlite3" && strings.Contains(joined, "submitted_head_sha=") && strings.Contains(joined, advanced) {
			databaseAligned = true
		}
	}
	if !fetched || !gateAligned || !databaseAligned {
		t.Fatalf("fetch=%v gate=%v database=%v", fetched, gateAligned, databaseAligned)
	}
}

func TestRecoverKeepLocalRollsBackGateWhenDatabaseCASFails(t *testing.T) {
	const (
		branch   = "copy/hero-request-a-service"
		oldGate  = "37c93f2081cead9d12bab41d21d4a09d652ffabd"
		advanced = "18704b1fc56bf84c1183ee004d6f371447bc93a6"
	)
	runner := &recoveryRunner{
		scenario:     recoveryScenario{run: "01M2C018A26RY2Y5GFRKC5NXCS", repo: recoveryRepo, branch: branch, recorded: oldGate, submitted: oldGate},
		patchesEqual: true,
		alreadyOwned: true,
		localHead:    advanced,
		failDatabase: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil); err == nil {
		t.Fatal("failed database compare-and-swap reported recovery success")
	}
	if runner.gateAligned {
		t.Fatal("gate ref remained advanced after database compare-and-swap failed")
	}
}

func TestRecoverKeepLocalFinishesInterruptedUserOwnedAlignment(t *testing.T) {
	const (
		branch   = "copy/hero-request-a-service"
		oldGate  = "37c93f2081cead9d12bab41d21d4a09d652ffabd"
		advanced = "18704b1fc56bf84c1183ee004d6f371447bc93a6"
	)
	runner := &recoveryRunner{
		scenario:     recoveryScenario{run: "01M2C018A26RY2Y5GFRKC5NXCS", repo: recoveryRepo, branch: branch, recorded: oldGate, submitted: oldGate},
		alreadyOwned: true,
		localHead:    advanced,
		gateAligned:  true,
		anchorExists: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Head != advanced || !runner.gateAligned || !runner.databaseAligned {
		t.Fatalf("result=%+v gate=%v database=%v", result, runner.gateAligned, runner.databaseAligned)
	}
	for _, request := range runner.requests {
		joined := request.Name + " " + strings.Join(request.Args, " ")
		if strings.HasPrefix(joined, "git update-ref refs/heads/"+branch+" ") {
			t.Fatalf("already advanced gate moved again: %s", joined)
		}
	}
}

func TestRecoverKeepLocalAlignsUserOwnedMultiCommitAdvance(t *testing.T) {
	const (
		branch   = "feat/free-tier-credit-allowance"
		oldGate  = "24c8f2824c388acb5cda3e8112fbf3ad81f51d4e"
		advanced = "19f810091bc0842b60016f055e6f85f1ee38c3de"
	)
	runner := &recoveryRunner{
		scenario:     recoveryScenario{run: "01M2DBYDSV6QQS23RPNV12VCHX", repo: recoveryRepo, branch: branch, recorded: oldGate, submitted: oldGate},
		alreadyOwned: true,
		localHead:    advanced,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Head != advanced || !runner.gateAligned || !runner.databaseAligned {
		t.Fatalf("result=%+v gate=%v database=%v", result, runner.gateAligned, runner.databaseAligned)
	}
}
