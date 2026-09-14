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
	scenario               recoveryScenario
	ancestral              bool
	alreadyOwned           bool
	localHead              string
	syncChecks             int
	gateAligned            bool
	databaseAligned        bool
	recordedReplaced       bool
	failDatabase           bool
	failAfterCommit        bool
	failRollback           bool
	failPostCheck          bool
	anchorExists           bool
	anchorHead             string
	anchorSymbolic         bool
	anchorSymbolicRace     bool
	anchorTargetMoved      bool
	nativeAnchor           bool
	nativeAnchorHead       string
	nativeAnchorWorktree   bool
	nativeWorktreeHead     string
	nonCommitHead          string
	nativeReturnsCustody   bool
	missingNativeAnchor    bool
	custodyReturned        bool
	requireExplicitBare    bool
	requireMatchingNullOID bool
	requests               []execx.Request
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

func (r *recoveryRunner) recoveryAnchorHead(ref string) string {
	if r.anchorHead != "" {
		return r.anchorHead
	}
	if strings.HasSuffix(ref, "/recorded") {
		return r.values().recorded
	}
	return r.values().submitted
}

func (r *recoveryRunner) nativeAnchorAt(request execx.Request) (string, bool) {
	if strings.HasSuffix(strings.ToLower(request.Dir), strings.ToLower(r.values().repo+".git")) {
		if !r.nativeAnchor {
			return "", false
		}
		if r.nativeAnchorHead != "" {
			return r.nativeAnchorHead, true
		}
		return r.values().recorded, true
	}
	if !r.nativeAnchorWorktree {
		return "", false
	}
	if r.nativeWorktreeHead != "" {
		return r.nativeWorktreeHead, true
	}
	if r.nativeAnchorHead != "" {
		return r.nativeAnchorHead, true
	}
	return r.values().recorded, true
}

func recoveryRequestCommand(request execx.Request) string {
	args := request.Args
	if request.Name == "git" && len(args) > 0 && strings.HasPrefix(args[0], "--git-dir=") {
		args = args[1:]
	}
	return request.Name + " " + strings.Join(args, " ")
}

func (r *recoveryRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	scenario := r.values()
	switch request.Name {
	case "sqlite3":
		sql := request.Args[len(request.Args)-1]
		if strings.Contains(sql, "SELECT changes()") {
			if r.localHead != "" && strings.Contains(sql, "UPDATE runs SET head_sha='"+r.localHead+"'") {
				if r.failDatabase {
					return execx.Result{Stdout: []byte(`[{"n":0}]`)}, nil
				}
				r.databaseAligned = true
				if r.failAfterCommit {
					r.failAfterCommit = false
					return execx.Result{}, errors.New("sqlite process ended after commit")
				}
			} else if r.localHead != "" && strings.Contains(sql, "UPDATE runs SET head_sha='"+scenario.submitted+"'") && strings.Contains(sql, "AND head_sha='"+r.localHead+"'") {
				if r.failRollback {
					return execx.Result{Stdout: []byte(`[{"n":0}]`)}, nil
				}
				r.databaseAligned = false
			} else if strings.Contains(sql, "UPDATE runs SET head_sha='"+scenario.submitted+"', submitted_head_sha='"+scenario.submitted+"'") {
				r.recordedReplaced = true
			}
			return execx.Result{Stdout: []byte(`[{"n":1}]`)}, nil
		}
		recorded := scenario.recorded
		submitted := scenario.submitted
		if r.recordedReplaced {
			recorded = scenario.submitted
		}
		if r.databaseAligned {
			recorded = r.localHead
			submitted = r.localHead
		}
		custodyReturnedAt := "0"
		if r.custodyReturned {
			custodyReturnedAt = "1"
		}
		return execx.Result{Stdout: []byte(`[{
			"run_id":"` + scenario.run + `",
			"repo_id":"` + scenario.repo + `",
			"branch":"` + scenario.branch + `",
			"status":"failed",
			"recorded_head":"` + recorded + `",
			"submitted_head":"` + submitted + `",
			"pushed_head":"",
			"custody_returned_at":` + custodyReturnedAt + `
		}]`)}, nil
	case "git":
		args := request.Args
		if strings.HasSuffix(strings.ToLower(request.Dir), strings.ToLower(scenario.repo+".git")) {
			explicit := "--git-dir=" + request.Dir
			if len(args) > 0 && args[0] == explicit {
				args = args[1:]
			} else if r.requireExplicitBare {
				return execx.Result{}, errors.New("bare repository was not explicit")
			}
		}
		joined := strings.Join(args, " ")
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
		case strings.HasPrefix(joined, "cat-file -e "):
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "cat-file -t "):
			head := strings.TrimPrefix(joined, "cat-file -t ")
			if head == r.nonCommitHead {
				return execx.Result{Stdout: []byte("blob\n")}, nil
			}
			return execx.Result{Stdout: []byte("commit\n")}, nil
		case strings.HasPrefix(joined, "merge-base --is-ancestor ") && r.ancestral:
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "merge-base --is-ancestor "):
			return execx.Result{ExitCode: 1}, nil
		case strings.HasPrefix(joined, "symbolic-ref --quiet refs/no-mistakes/recovery/") && r.anchorSymbolic:
			return execx.Result{Stdout: []byte("refs/heads/" + scenario.branch + "\n")}, nil
		case strings.HasPrefix(joined, "symbolic-ref --quiet refs/no-mistakes/"):
			return execx.Result{ExitCode: 1}, nil
		case strings.HasPrefix(joined, "show-ref --verify --hash refs/no-mistakes/recovery/") && r.anchorExists:
			ref := strings.TrimPrefix(joined, "show-ref --verify --hash ")
			return execx.Result{Stdout: []byte(r.recoveryAnchorHead(ref) + "\n")}, nil
		case strings.HasPrefix(joined, "show-ref --verify --hash refs/no-mistakes/recovery/"):
			return execx.Result{ExitCode: 1}, nil
		case strings.HasPrefix(joined, "show-ref --verify --hash refs/no-mistakes/recover/"):
			if head, found := r.nativeAnchorAt(request); found {
				return execx.Result{Stdout: []byte(head + "\n")}, nil
			}
			return execx.Result{ExitCode: 1}, nil
		case strings.HasPrefix(joined, "fetch --no-tags --no-write-fetch-head "):
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "update-ref refs/heads/"+scenario.branch+" "+r.localHead+" "+scenario.submitted):
			r.gateAligned = true
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "update-ref refs/heads/"+scenario.branch+" "+scenario.submitted+" "+r.localHead):
			r.gateAligned = false
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "update-ref --no-deref refs/no-mistakes/recovery/"):
			if r.anchorSymbolicRace {
				return execx.Result{ExitCode: 1}, nil
			}
			parts := strings.Fields(joined)
			if r.requireMatchingNullOID && (len(parts) != 5 || parts[4] != strings.Repeat("0", len(parts[3]))) {
				return execx.Result{ExitCode: 1}, nil
			}
			r.anchorExists = true
			r.anchorHead = parts[3]
			return execx.Result{}, nil
		case strings.HasPrefix(joined, "update-ref refs/no-mistakes/recovery/"):
			if r.anchorSymbolicRace {
				r.anchorTargetMoved = true
				return execx.Result{}, nil
			}
			r.anchorExists = true
			return execx.Result{}, nil
		}
	case "no-mistakes":
		if strings.Join(request.Args, " ") == "axi sync --check" {
			r.syncChecks++
			if !r.alreadyOwned && r.syncChecks == 1 {
				return execx.Result{ExitCode: 1}, nil
			}
			if r.failPostCheck && r.databaseAligned {
				return execx.Result{ExitCode: 1}, nil
			}
			head := r.localHead
			if head == "" {
				head = scenario.submitted
			}
			pipelineHead := scenario.submitted
			currentHead := scenario.submitted
			if r.custodyReturned {
				currentHead = scenario.recorded
			}
			if r.gateAligned {
				currentHead = head
			}
			if r.databaseAligned {
				pipelineHead = head
				currentHead = head
			}
			state := "user_owned"
			if r.custodyReturned {
				state = "custody_returned"
			}
			return execx.Result{Stdout: []byte("branch_sync:\n  state: " + state + "\n  safety: " + state + "\n  local:\n    head: " + head + "\n    clean: true\n  pipeline:\n    run: \"" + scenario.run + "\"\n    submitted_head: " + pipelineHead + "\n    current_head: " + currentHead + "\n")}, nil
		}
		if r.nativeReturnsCustody {
			r.custodyReturned = true
			if !r.missingNativeAnchor {
				r.nativeAnchorWorktree = true
			}
		}
		return execx.Result{Stdout: []byte("custody returned\n")}, nil
	}
	return execx.Result{}, errors.New("unexpected command: " + recoveryRequestCommand(request))
}

func TestRecoverKeepLocalRepairsAncestralRebaseGateShapeWithoutReset(t *testing.T) {
	runner := &recoveryRunner{ancestral: true}
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
		joined := recoveryRequestCommand(request)
		if strings.Contains(joined, " reset ") || strings.HasSuffix(joined, " reset") {
			t.Fatalf("destructive reset invoked: %s", joined)
		}
		if strings.HasPrefix(joined, "git update-ref --no-deref refs/no-mistakes/recovery/"+recoveryRun+"/recorded "+recoveryRecorded) {
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

func TestRecoverKeepLocalUsesAnExplicitBareRepositoryBoundary(t *testing.T) {
	runner := &recoveryRunner{ancestral: true, requireExplicitBare: true}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", recoveryBranch, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverKeepLocalCreatesSHA256RecoveryAnchor(t *testing.T) {
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       recoveryRun,
			repo:      recoveryRepo,
			branch:    recoveryBranch,
			recorded:  strings.Repeat("a", 64),
			submitted: strings.Repeat("b", 64),
		},
		ancestral:              true,
		requireMatchingNullOID: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", recoveryBranch, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverKeepLocalRefusesNonAncestralCommitsBeforeMutation(t *testing.T) {
	runner := &recoveryRunner{}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", recoveryBranch, nil); err == nil {
		t.Fatal("non-equivalent divergence accepted")
	}
	for _, request := range runner.requests {
		joined := recoveryRequestCommand(request)
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
		joined := recoveryRequestCommand(request)
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

func TestRecoverKeepLocalVerifiesCommittedDatabaseCASBeforeRollback(t *testing.T) {
	const (
		branch   = "copy/hero-request-a-service"
		oldGate  = "37c93f2081cead9d12bab41d21d4a09d652ffabd"
		advanced = "18704b1fc56bf84c1183ee004d6f371447bc93a6"
	)
	runner := &recoveryRunner{
		scenario:        recoveryScenario{run: "01M2C018A26RY2Y5GFRKC5NXCS", repo: recoveryRepo, branch: branch, recorded: oldGate, submitted: oldGate},
		alreadyOwned:    true,
		localHead:       advanced,
		failAfterCommit: true,
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
		joined := recoveryRequestCommand(request)
		if strings.HasPrefix(joined, "git update-ref refs/heads/"+branch+" "+oldGate+" "+advanced) {
			t.Fatalf("verified committed database CAS rolled gate back: %s", joined)
		}
	}
}

func TestRecoverKeepLocalPreservesAlignedGateWhenDatabaseRollbackFails(t *testing.T) {
	const (
		branch   = "copy/hero-request-a-service"
		oldGate  = "37c93f2081cead9d12bab41d21d4a09d652ffabd"
		advanced = "18704b1fc56bf84c1183ee004d6f371447bc93a6"
	)
	runner := &recoveryRunner{
		scenario:      recoveryScenario{run: "01M2C018A26RY2Y5GFRKC5NXCS", repo: recoveryRepo, branch: branch, recorded: oldGate, submitted: oldGate},
		alreadyOwned:  true,
		localHead:     advanced,
		failRollback:  true,
		failPostCheck: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil); err == nil {
		t.Fatal("failed post-alignment check reported recovery success")
	}
	if !runner.gateAligned || !runner.databaseAligned {
		t.Fatalf("failed rollback split aligned state: gate=%v database=%v", runner.gateAligned, runner.databaseAligned)
	}
	runner.failRollback = false
	runner.failPostCheck = false
	result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Head != advanced || !runner.gateAligned || !runner.databaseAligned {
		t.Fatalf("retry result=%+v gate=%v database=%v", result, runner.gateAligned, runner.databaseAligned)
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
		joined := recoveryRequestCommand(request)
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

func TestRecoverKeepLocalAlignsV175CustodyReturnedGateBeforeFreshRun(t *testing.T) {
	const (
		run       = "01M2G34VN0E2764T0AY3NABZH0"
		branch    = "fix/cfo-pr-status-context"
		staleGate = "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83"
		recorded  = "c5fcb965b84750629f8a87f35ee571a128ec7e4e"
		local     = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	)
	runner := &recoveryRunner{
		scenario:        recoveryScenario{run: run, repo: recoveryRepo, branch: branch, recorded: recorded, submitted: staleGate},
		alreadyOwned:    true,
		localHead:       local,
		nativeAnchor:    true,
		custodyReturned: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != run || result.Head != local || !runner.gateAligned || !runner.databaseAligned {
		t.Fatalf("result=%+v gate=%v database=%v", result, runner.gateAligned, runner.databaseAligned)
	}
	var preservedGate, verifiedGate bool
	for _, request := range runner.requests {
		joined := recoveryRequestCommand(request)
		if strings.Contains(joined, " reset ") || joined == "no-mistakes axi sync --recover --keep-local" {
			t.Fatalf("unsafe recovery command: %s", joined)
		}
		if strings.HasPrefix(joined, "git update-ref --no-deref refs/no-mistakes/recovery/"+run+"/gate "+staleGate) {
			preservedGate = true
		}
		if joined == "git show-ref --verify --hash refs/no-mistakes/recovery/"+run+"/gate" && preservedGate {
			verifiedGate = true
		}
		if strings.HasPrefix(joined, "git update-ref refs/heads/"+branch+" ") || request.Name == "sqlite3" && strings.Contains(joined, "UPDATE runs") {
			if !verifiedGate {
				t.Fatalf("CFO head move preceded exact gate-anchor verification: %s", joined)
			}
		}
	}
	if !preservedGate || !verifiedGate {
		t.Fatalf("preserved=%v verified=%v", preservedGate, verifiedGate)
	}
}

func TestRecoverKeepLocalPreservesCFOEvidenceWhenNativeAndTargetHeadsMatch(t *testing.T) {
	const (
		aligned   = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
		staleGate = "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83"
	)
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  aligned,
			submitted: staleGate,
		},
		alreadyOwned:    true,
		localHead:       aligned,
		nativeAnchor:    true,
		custodyReturned: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	for attempt := 1; attempt <= 2; attempt++ {
		result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if result.Head != aligned || !runner.gateAligned || !runner.databaseAligned || !runner.anchorExists {
			t.Fatalf("attempt %d result=%+v gate=%v database=%v anchor=%v", attempt, result, runner.gateAligned, runner.databaseAligned, runner.anchorExists)
		}
	}
}

func TestRecoverKeepLocalAcceptsNativeAlignedCustodyReturnedWithoutGateAnchor(t *testing.T) {
	const head = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  head,
			submitted: head,
		},
		localHead:            head,
		nativeReturnsCustody: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	for attempt := 1; attempt <= 2; attempt++ {
		result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if result.Head != head {
			t.Fatalf("attempt %d result=%+v", attempt, result)
		}
	}
	if !runner.nativeAnchorWorktree || runner.nativeAnchor {
		t.Fatalf("native anchor locations: worktree=%v bare=%v", runner.nativeAnchorWorktree, runner.nativeAnchor)
	}
	for _, request := range runner.requests {
		joined := recoveryRequestCommand(request)
		if strings.HasPrefix(joined, "git update-ref --no-deref refs/no-mistakes/recovery/"+runner.scenario.run+"/gate ") {
			t.Fatalf("native-aligned recovery created CFO swap evidence: %s", joined)
		}
	}
}

func TestRecoverKeepLocalRefusesConflictingNativeAnchorsAcrossRepositories(t *testing.T) {
	const head = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  head,
			submitted: head,
		},
		alreadyOwned:         true,
		localHead:            head,
		nativeAnchor:         true,
		nativeAnchorWorktree: true,
		nativeWorktreeHead:   "c5fcb965b84750629f8a87f35ee571a128ec7e4e",
		custodyReturned:      true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil); err == nil || !strings.Contains(err.Error(), "conflict across repositories") {
		t.Fatalf("conflicting native anchors accepted: %v", err)
	}
	if runner.gateAligned || runner.databaseAligned {
		t.Fatalf("conflicting native anchors mutated state: gate=%v database=%v", runner.gateAligned, runner.databaseAligned)
	}
}

func TestRecoverKeepLocalRejectsSymbolicRecoveryAnchorBeforeMovement(t *testing.T) {
	const (
		stale = "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83"
		local = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	)
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  "c5fcb965b84750629f8a87f35ee571a128ec7e4e",
			submitted: stale,
		},
		alreadyOwned:    true,
		localHead:       local,
		anchorExists:    true,
		anchorSymbolic:  true,
		nativeAnchor:    true,
		custodyReturned: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil); err == nil || !strings.Contains(err.Error(), "must not be symbolic") {
		t.Fatalf("symbolic recovery anchor accepted: %v", err)
	}
	if runner.gateAligned || runner.databaseAligned {
		t.Fatalf("symbolic recovery anchor mutated state: gate=%v database=%v", runner.gateAligned, runner.databaseAligned)
	}
}

func TestRecoverKeepLocalRejectsNonCommitRecoveryAnchor(t *testing.T) {
	const (
		head = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
		blob = "7172ecca6dd4b05782f0440f6a565badae04a1aa"
	)
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  head,
			submitted: head,
		},
		alreadyOwned:    true,
		localHead:       head,
		anchorExists:    true,
		anchorHead:      blob,
		nonCommitHead:   blob,
		nativeAnchor:    true,
		custodyReturned: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil); err == nil || !strings.Contains(err.Error(), "must name a commit directly") {
		t.Fatalf("non-commit recovery anchor accepted: %v", err)
	}
}

func TestRecoverKeepLocalNoDerefCreationCannotMoveSymbolicTarget(t *testing.T) {
	const (
		stale = "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83"
		local = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	)
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  stale,
			submitted: stale,
		},
		alreadyOwned:       true,
		localHead:          local,
		anchorSymbolicRace: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil); err == nil {
		t.Fatal("recovery unexpectedly succeeded through anchor creation race")
	}
	if runner.anchorTargetMoved || runner.gateAligned || runner.databaseAligned {
		t.Fatalf("anchor creation race moved state: target=%v gate=%v database=%v", runner.anchorTargetMoved, runner.gateAligned, runner.databaseAligned)
	}
}

func TestRecoverKeepLocalRefusesNativeCustodyWithMissingOrConflictingAnchor(t *testing.T) {
	const head = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	for _, test := range []struct {
		name                string
		missingNativeAnchor bool
		nativeAnchorHead    string
	}{
		{name: "missing", missingNativeAnchor: true},
		{name: "conflicting", nativeAnchorHead: "c5fcb965b84750629f8a87f35ee571a128ec7e4e"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &recoveryRunner{
				scenario: recoveryScenario{
					run:       "01M2G34VN0E2764T0AY3NABZH0",
					repo:      recoveryRepo,
					branch:    "fix/cfo-pr-status-context",
					recorded:  head,
					submitted: head,
				},
				localHead:            head,
				nativeReturnsCustody: true,
				missingNativeAnchor:  test.missingNativeAnchor,
				nativeAnchorHead:     test.nativeAnchorHead,
			}
			reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
			if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil); err == nil {
				t.Fatal("unsafe native custody accepted")
			}
		})
	}
}

func TestRecoverKeepLocalRefusesV175CustodyReturnedWithoutNativeAnchor(t *testing.T) {
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  "c5fcb965b84750629f8a87f35ee571a128ec7e4e",
			submitted: "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83",
		},
		alreadyOwned:    true,
		localHead:       "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4",
		custodyReturned: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil); err == nil {
		t.Fatal("unanchored returned pipeline head was accepted")
	}
	for _, request := range runner.requests {
		joined := recoveryRequestCommand(request)
		if strings.Contains(joined, "update-ref") || request.Name == "sqlite3" && strings.Contains(joined, "UPDATE runs") {
			t.Fatalf("unsafe recovery mutated state: %s", joined)
		}
	}
}

func TestRecoverKeepLocalRefusesAlignedV175CustodyReturnedWithoutNativeAnchor(t *testing.T) {
	const head = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  head,
			submitted: head,
		},
		alreadyOwned:    true,
		localHead:       head,
		anchorExists:    true,
		anchorHead:      "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83",
		custodyReturned: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil); err == nil || !strings.Contains(err.Error(), "not anchored") {
		t.Fatalf("aligned unanchored custody return accepted: %v", err)
	}
	for _, request := range runner.requests {
		joined := recoveryRequestCommand(request)
		if strings.Contains(joined, "update-ref") || request.Name == "sqlite3" && strings.Contains(joined, "UPDATE runs") {
			t.Fatalf("unsafe recovery mutated state: %s", joined)
		}
	}
}

func TestRecoverKeepLocalFinishesCommittedCustodyReturnedAlignment(t *testing.T) {
	const (
		run       = "01M2G34VN0E2764T0AY3NABZH0"
		branch    = "fix/cfo-pr-status-context"
		staleGate = "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83"
		recorded  = "c5fcb965b84750629f8a87f35ee571a128ec7e4e"
		local     = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	)
	runner := &recoveryRunner{
		scenario:        recoveryScenario{run: run, repo: recoveryRepo, branch: branch, recorded: recorded, submitted: staleGate},
		alreadyOwned:    true,
		localHead:       local,
		gateAligned:     true,
		databaseAligned: true,
		anchorExists:    true,
		nativeAnchor:    true,
		custodyReturned: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	result, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != run || result.Head != local {
		t.Fatalf("result=%+v", result)
	}
	for _, request := range runner.requests {
		joined := recoveryRequestCommand(request)
		if strings.Contains(joined, "update-ref") || request.Name == "sqlite3" && strings.Contains(joined, "UPDATE runs") {
			t.Fatalf("aligned retry mutated state: %s", joined)
		}
	}
}

func TestRecoverKeepLocalRefusesCommittedCustodyAlignmentWithConflictingGateAnchor(t *testing.T) {
	const aligned = "e78eae6f6569b7bc37835ae36b4ef79e4e434fd4"
	runner := &recoveryRunner{
		scenario: recoveryScenario{
			run:       "01M2G34VN0E2764T0AY3NABZH0",
			repo:      recoveryRepo,
			branch:    "fix/cfo-pr-status-context",
			recorded:  aligned,
			submitted: "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83",
		},
		alreadyOwned:    true,
		localHead:       aligned,
		gateAligned:     true,
		databaseAligned: true,
		anchorExists:    true,
		anchorHead:      aligned,
		nativeAnchor:    true,
		custodyReturned: true,
	}
	reader := Reader{Commands: runner, Root: `C:\Users\fpres\.no-mistakes`}
	if _, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil); err == nil {
		t.Fatal("aligned custody retry accepted a non-stale CFO gate anchor")
	}
}
