package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kunchenguid/no-mistakes/internal/branchsync"
	"github.com/kunchenguid/no-mistakes/internal/db"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/pipeline/steps"
	"github.com/kunchenguid/no-mistakes/internal/types"
	"github.com/kunchenguid/no-mistakes/internal/winproc"
)

func TestControllerInheritedPublicationJourney(t *testing.T) {
	var calls atomic.Int32
	manager, database, repo, params, gate := authoredAdmissionFixture(t, func() []pipeline.Step {
		var result []pipeline.Step
		for _, step := range steps.AllSteps() {
			result = append(result, &authoredFixtureStep{name: step.Name(), calls: &calls})
		}
		return result
	})
	gitCmd(t, repo.WorkingPath, "config", "url."+filepath.ToSlash(gate)+".insteadOf", repo.UpstreamURL)
	parent, err := database.GetRun(params.Request.PredecessorID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateRunPushBinding(parent.ID, db.PushBinding{HeadSHA: parent.HeadSHA, TargetKind: "upstream", TargetFingerprint: branchsync.TargetFingerprint(repo.UpstreamURL), Ref: "refs/heads/feature"}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateRunPRURL(parent.ID, "https://github.com/test/repo/pull/17"); err != nil {
		t.Fatal(err)
	}
	review, err := database.InsertStepResult(parent.ID, types.StepReview)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.StartStepWithAutoFixLimit(review.ID, 2); err != nil {
		t.Fatal(err)
	}
	findings, selected := `{"findings":[{"id":"r1","description":"retained original failure"}]}`, `["r1"]`
	for index, source := range []string{db.RoundSelectionSourceAutoFix, db.RoundSelectionSourceUser} {
		round, err := database.InsertStepRound(review.ID, index*2+1, "initial", &findings, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.SetStepRoundSelection(round.ID, &selected, source); err != nil {
			t.Fatal(err)
		}
		if _, err := database.InsertStepRound(review.ID, index*2+2, "auto_fix", nil, nil, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.FailStep(review.ID, "retained original failure", 4); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.ReadTerminalSnapshot(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	params.Request.SnapshotDigest = snapshot.Digest()
	params.Request.PublishedHeadSHA = *snapshot.Run.LastPushedSHA
	params.Request.PushGeneration = *snapshot.Run.PushGeneration
	params.Request.PushTargetFingerprint = *snapshot.Run.PushTargetFingerprint
	proof, err := params.Request.LaunchAssertion.Verify(params.Request.LaunchAssertion.TrustedSHA, params.Request.LaunchAssertion.Profiles)
	if err != nil {
		t.Fatal(err)
	}
	event, _, err := database.AcceptValidationAdmission(context.Background(), db.ValidationAdmission{Request: params.Request, Snapshot: *snapshot, AssertionProof: proof, WorktreeRoot: filepath.Dir(manager.paths.WorktreeDir(repo.ID, "pending")), Gates: "[]", Steps: types.AllSteps(), Operator: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, gate, "fetch", "--no-tags", "--no-write-fetch-head", repo.WorkingPath, params.Request.CandidateSHA)
	gitCmd(t, gate, "update-ref", "refs/no-mistakes/authored-candidate/"+event.ID, params.Request.CandidateSHA)
	child, err := database.GetRun(event.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, gate, "worktree", "add", "--detach", *child.WorktreeDir, child.HeadSHA)
	if err := database.UpdateRunStatus(child.ID, types.RunRunning); err != nil {
		t.Fatal(err)
	}
	step, err := database.InsertStepResult(child.ID, types.StepTest)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.StartStepWithAutoFixLimit(step.ID, 0); err != nil {
		t.Fatal(err)
	}
	preserveStaleRunHeads(database, manager.paths, nil)
	if _, err := database.RecoverStaleRunsExcept("daemon crashed during execution", nil); err != nil {
		t.Fatal(err)
	}
	child, err = database.GetRun(child.ID)
	if err != nil || child.TerminalHeadVerifiedAt == nil {
		t.Fatalf("restart: %+v %v", child, err)
	}
	beforeChild, err := database.ReadTerminalSnapshot(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeFamily, err := database.ValidationFamily(child.ID)
	if err != nil || beforeFamily.Spending[types.StepReview].Automatic != 1 || beforeFamily.Spending[types.StepReview].Manual != 1 {
		t.Fatalf("original spending: %+v %v", beforeFamily, err)
	}
	service := &branchsync.Service{DB: database, Repo: repo, WorkDir: repo.WorkingPath, GateDir: gate, Paths: manager.paths}
	if err := os.WriteFile(filepath.Join(manager.paths.Root(), ".controller-recovery-fixture"), []byte("isolated native admission fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	{
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Getenv("CFO_RECOVERY_CONTROLLER_TESTS"), "-test.run", "^TestRecoverInheritedPublicationNativeFixture$", "-test.v", "-test.timeout", "140s")
		command.Dir = filepath.Join(os.Getenv("CFO_RECOVERY_NATIVE_CONTROLLER"), "internal", "pipeline")
		winproc.Harden(command)
		command.Env = append(os.Environ(), "CFO_RECOVERY_NATIVE_ROOT="+manager.paths.Root(), "CFO_RECOVERY_NATIVE_PROJECT="+repo.WorkingPath, "CFO_RECOVERY_NATIVE_RUN="+child.ID, "CFO_RECOVERY_NATIVE_ADMISSION="+event.ID, "CFO_RECOVERY_NATIVE_HEAD="+child.HeadSHA)
		output, err := command.CombinedOutput()
		t.Logf("controller boundary:\n%s", output)
		if err != nil {
			t.Fatalf("controller boundary: %v", err)
		}
	}
	recovered := service.InspectCached(context.Background())
	t.Logf("native custody after controller: %+v", recovered)
	child, err = database.GetRun(child.ID)
	if err != nil || recovered.State != "local_ahead" || recovered.Changed || child.CustodyReturnedAt == nil {
		t.Fatalf("native recovery failed: %+v child=%+v error=%v", recovered, child, err)
	}
	if *child.LastPushedSHA != *snapshot.Run.LastPushedSHA || *child.PushGeneration != *snapshot.Run.PushGeneration || gitOutput(t, repo.WorkingPath, "rev-parse", "HEAD") != params.Request.CandidateSHA || gitOutput(t, gate, "rev-parse", "refs/heads/feature") != parent.HeadSHA {
		t.Fatal("recovery changed source or publication")
	}
	afterChild, err := database.ReadTerminalSnapshot(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterChild.Run.CustodyReturnedAt = nil
	afterChild.Run.UpdatedAt = beforeChild.Run.UpdatedAt
	if !reflect.DeepEqual(beforeChild, afterChild) {
		t.Fatal("custody recovery rewrote child history")
	}
	afterFamily, err := database.ValidationFamily(child.ID)
	if err != nil || !reflect.DeepEqual(beforeFamily, afterFamily) {
		t.Fatalf("recovery reset spending: %v", err)
	}

	// A new correction must retain this failed child's exact launch, source and
	// publication bindings. Required stages start fresh without repairs.
	failed, err := database.ReadTerminalSnapshot(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.WorkingPath, "continuation.txt"), []byte("authored continuation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo.WorkingPath, "add", "continuation.txt")
	gitCmd(t, repo.WorkingPath, "commit", "-m", "retain interrupted candidate in correction")
	params.Request.PredecessorID, params.Request.SnapshotDigest = child.ID, failed.Digest()
	params.Request.LaunchNonce, params.Request.ValidationGeneration = *child.LaunchNonce, *child.LaunchValidationGeneration
	params.Request.CandidateSHA = gitOutput(t, repo.WorkingPath, "rev-parse", "HEAD")
	params.Request.CandidateTree = gitOutput(t, repo.WorkingPath, "rev-parse", "HEAD^{tree}")
	originalProvider := authoredPRCommandOutput
	t.Cleanup(func() { authoredPRCommandOutput = originalProvider })
	authoredPRCommandOutput = func(*exec.Cmd) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"html_url":%q,"state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"test/repo"}},"base":{"ref":"main","repo":{"full_name":"test/repo"}}}`, *child.PRURL, *child.LastPushedSHA)), nil
	}
	for _, change := range []string{"snapshot", "publication", "generation", "launch", "tree"} {
		t.Run("continuation refuses "+change, func(t *testing.T) {
			invalid := params
			switch change {
			case "snapshot":
				invalid.Request.SnapshotDigest = "stale"
			case "publication":
				invalid.Request.PublishedHeadSHA = params.Request.CandidateSHA
			case "generation":
				invalid.Request.PushGeneration++
			case "launch":
				invalid.Request.LaunchNonce = "stale"
			case "tree":
				invalid.Request.CandidateTree = child.HeadSHA
			}
			if _, err := manager.validateAuthored(context.Background(), invalid, os.Getpid()); err == nil {
				t.Fatal("unbound continuation admitted")
			}
		})
	}
	continuation, err := manager.validateAuthored(context.Background(), params, os.Getpid())
	if err != nil || !continuation.Started {
		t.Fatalf("bound continuation: %+v %v", continuation, err)
	}
	finished := waitForRunTerminalState(t, database, continuation.Receipt.RunID)
	manager.wg.Wait()
	if finished.Status != types.RunCompleted || calls.Load() != int32(len(steps.AllSteps())) || continuation.Family.HasChildRepairs || !reflect.DeepEqual(beforeFamily.Spending, continuation.Family.Spending) {
		t.Fatalf("continuation source/stages/spending: %+v calls=%d family=%+v", finished, calls.Load(), continuation.Family)
	}
	finalChild, err := database.ReadTerminalSnapshot(child.ID)
	if err != nil || !reflect.DeepEqual(failed, finalChild) {
		t.Fatal("continuation changed failed history")
	}
	finalParent, err := database.ReadTerminalSnapshot(parent.ID)
	if err != nil || !reflect.DeepEqual(snapshot, finalParent) {
		t.Fatal("journey changed predecessor history")
	}
}

func TestControllerAuthoredCancellationRemainsRefused(t *testing.T) {
	manager, database, repo, params, gate := authoredAdmissionFixture(t, nil)
	parent, err := database.GetRun(params.Request.PredecessorID)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateRunStatusWithVerifiedHead(parent.ID, types.RunCancelled, parent.HeadSHA); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.ReadTerminalSnapshot(params.Request.PredecessorID)
	if err != nil {
		t.Fatal(err)
	}
	params.Request.SnapshotDigest = snapshot.Digest()
	proof, err := params.Request.LaunchAssertion.Verify(params.Request.LaunchAssertion.TrustedSHA, params.Request.LaunchAssertion.Profiles)
	if err != nil {
		t.Fatal(err)
	}
	event, _, err := database.AcceptValidationAdmission(context.Background(), db.ValidationAdmission{Request: params.Request, Snapshot: *snapshot, AssertionProof: proof, WorktreeRoot: filepath.Dir(manager.paths.WorktreeDir(repo.ID, "pending")), Gates: "[]", Steps: types.AllSteps(), Operator: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, gate, "fetch", "--no-tags", "--no-write-fetch-head", repo.WorkingPath, params.Request.CandidateSHA)
	gitCmd(t, gate, "update-ref", "refs/no-mistakes/authored-candidate/"+event.ID, params.Request.CandidateSHA)
	if err := database.UpdateRunStatusWithVerifiedHead(event.ChildID, types.RunCancelled, params.Request.CandidateSHA); err != nil {
		t.Fatal(err)
	}
	gateHead := gitOutput(t, gate, "rev-parse", "refs/heads/feature")
	if err := os.WriteFile(filepath.Join(manager.paths.Root(), ".controller-recovery-fixture"), []byte("isolated native admission fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("CFO_RECOVERY_CONTROLLER_TESTS"), "-test.run", "^TestRecoverInheritedPublicationNativeFixture$", "-test.v", "-test.timeout", "25s")
	command.Dir = filepath.Join(os.Getenv("CFO_RECOVERY_NATIVE_CONTROLLER"), "internal", "pipeline")
	winproc.Harden(command)
	command.Env = append(os.Environ(), "CFO_RECOVERY_NATIVE_ROOT="+manager.paths.Root(), "CFO_RECOVERY_NATIVE_PROJECT="+repo.WorkingPath, "CFO_RECOVERY_NATIVE_RUN="+event.ChildID, "CFO_RECOVERY_NATIVE_HEAD="+params.Request.CandidateSHA, "CFO_RECOVERY_EXPECT_USER_OWNED=1")
	output, err := command.CombinedOutput()
	t.Logf("separate user-owned cancellation:\n%s", output)
	if err != nil || gitOutput(t, gate, "rev-parse", "refs/heads/feature") != gateHead {
		t.Fatalf("user-owned cancellation changed: %v", err)
	}
}
