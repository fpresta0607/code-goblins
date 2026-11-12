package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline/pipelinetest"
	"gopkg.in/yaml.v3"
)

type authoredRecoveryFixture struct {
	reader                  Reader
	database                *sql.DB
	caller, bare, managed   string
	candidate, tree, parent string
	isPublished             bool
	recoveries              int
	nativeFailure           string
	postMutation            func()
}

func newAuthoredRecoveryFixture(t *testing.T, isPublished bool) *authoredRecoveryFixture {
	t.Helper()
	for _, tool := range []string{"git", "sqlite3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required CI fixture tool %s: %v", tool, err)
		}
	}
	fixture := &authoredRecoveryFixture{caller: t.TempDir(), isPublished: isPublished}
	root := t.TempDir()
	fixture.bare = filepath.Join(root, "repos", recoveryRepo+".git")
	fixture.managed = filepath.Join(root, "worktrees", recoveryRepo, recoveryRun)
	runPipelineGit(t, fixture.caller, "init", "--quiet", "-b", recoveryBranch)
	writeFixtureFile(t, filepath.Join(fixture.caller, "source.txt"), "retained predecessor\n")
	runPipelineGit(t, fixture.caller, "add", ".")
	runPipelineGit(t, fixture.caller, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "predecessor")
	fixture.parent = runPipelineGit(t, fixture.caller, "rev-parse", "HEAD")
	runPipelineGit(t, root, "clone", "--quiet", "--bare", fixture.caller, fixture.bare)
	writeFixtureFile(t, filepath.Join(fixture.caller, "source.txt"), "retained authored correction\n")
	runPipelineGit(t, fixture.caller, "add", ".")
	runPipelineGit(t, fixture.caller, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "authored correction")
	fixture.candidate = runPipelineGit(t, fixture.caller, "rev-parse", "HEAD")
	fixture.tree = runPipelineGit(t, fixture.caller, "rev-parse", "HEAD^{tree}")
	runPipelineGit(t, fixture.bare, "fetch", "--quiet", "--no-tags", fixture.caller, fixture.candidate)
	runPipelineGit(t, fixture.bare, "update-ref", "refs/no-mistakes/authored-candidate/admission-one", fixture.candidate)
	if isPublished {
		runPipelineGit(t, fixture.bare, "update-ref", "refs/no-mistakes/recover/"+recoveryRun, fixture.candidate)
	}
	runPipelineGit(t, fixture.bare, "worktree", "add", "--quiet", "--detach", fixture.managed, fixture.candidate)
	database, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	fixture.database = database
	t.Cleanup(func() { _ = database.Close() })
	fixture.execute(t, `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,last_pushed_sha TEXT,custody_returned_at INTEGER,created_at INTEGER,updated_at INTEGER,push_active INTEGER,terminal_head_verified_at INTEGER,terminal_source_state TEXT,worktree_dir TEXT,push_generation INTEGER,push_target_kind TEXT,push_target_fingerprint TEXT,push_ref TEXT,last_pushed_at INTEGER,pr_url TEXT,launch_nonce TEXT,launch_validation_generation TEXT,launch_intent_digest TEXT,intent TEXT,error TEXT);
CREATE TABLE validation_admissions(id TEXT,child_id TEXT,predecessor_id TEXT,event_json TEXT);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT,status TEXT,auto_fix_limit INTEGER,agent_pid INTEGER,error TEXT);
CREATE TABLE step_rounds(id TEXT,step_result_id TEXT,round INTEGER,trigger_type TEXT,selection_source TEXT,selected_finding_ids TEXT,starting_head_sha TEXT,reviewed_head_sha TEXT,duration_ms INTEGER);`)
	fixture.execute(t, "INSERT INTO repos VALUES(?,?)", recoveryRepo, fixture.caller)
	fixture.execute(t, "INSERT INTO runs(id,repo_id,branch,status,head_sha,submitted_head_sha,created_at,intent,error) VALUES('parent',?,?,'failed',?,?,1,'original intent','original Review failure')", recoveryRepo, recoveryBranch, fixture.parent, fixture.parent)
	fixture.execute(t, "INSERT INTO runs(id,repo_id,branch,status,head_sha,submitted_head_sha,created_at,updated_at,push_active,terminal_head_verified_at,terminal_source_state,worktree_dir,launch_nonce,launch_validation_generation,launch_intent_digest,intent) VALUES(?,?,?,'cancelled',?,?,2,3,0,4,?,?,'authored-nonce','frozen-generation','original-digest','original intent')", recoveryRun, recoveryRepo, recoveryBranch, fixture.candidate, fixture.candidate, "clean:"+fixture.candidate, fixture.managed)
	if isPublished {
		fixture.execute(t, "UPDATE runs SET last_pushed_sha=?,push_generation=1,push_target_kind='upstream',push_target_fingerprint='original-target',push_ref=?,last_pushed_at=1,pr_url='https://github.com/test/repo/pull/17'", fixture.parent, "refs/heads/"+recoveryBranch)
	}
	fixture.execute(t, "INSERT INTO step_results VALUES('old-review','parent','review','failed',2,NULL,'original Review failure'),('child-review',?,'review','failed',0,NULL,'cancelled at Review')", recoveryRun)
	fixture.execute(t, `INSERT INTO step_rounds VALUES('old-selected','old-review',1,'initial','user','["R1"]',NULL,NULL,71),('old-fix','old-review',2,'auto_fix',NULL,NULL,NULL,NULL,83),('child-round','child-review',1,'initial',NULL,NULL,?,?,19)`, fixture.candidate, fixture.candidate)
	var event string
	err = database.QueryRow(`SELECT json_object('ID','admission-one','ChildID',id,'Request',json_object('predecessor_id','parent','candidate_sha',head_sha,'candidate_tree',?,'caller_dir',?,'published_head_sha',COALESCE(last_pushed_sha,''),'push_generation',COALESCE(push_generation,0),'push_target_fingerprint',COALESCE(push_target_fingerprint,'')),'InitialChild',json_object('ID',id,'RepoID',repo_id,'Branch',branch,'HeadSHA',head_sha,'SubmittedHeadSHA',submitted_head_sha,'LastPushedSHA',last_pushed_sha,'PushGeneration',push_generation,'PushTargetKind',push_target_kind,'PushTargetFingerprint',push_target_fingerprint,'PushRef',push_ref,'LastPushedAt',last_pushed_at,'PRURL',pr_url,'LaunchNonce',launch_nonce,'LaunchValidationGeneration',launch_validation_generation,'LaunchIntentDigest',launch_intent_digest,'Intent',intent,'WorktreeDir',worktree_dir),'Snapshot',json_object('Run',json_object('HeadSHA',?,'SubmittedHeadSHA',?,'LastPushedSHA',last_pushed_sha,'PushGeneration',push_generation,'PushTargetKind',push_target_kind,'PushTargetFingerprint',push_target_fingerprint,'PushRef',push_ref,'LastPushedAt',last_pushed_at,'PRURL',pr_url))) FROM runs WHERE id=?`, fixture.tree, fixture.caller, fixture.parent, fixture.parent, recoveryRun).Scan(&event)
	if err != nil {
		t.Fatal(err)
	}
	fixture.execute(t, "INSERT INTO validation_admissions VALUES('admission-one',?,'parent',?)", recoveryRun, event)
	fixture.reader = Reader{Root: root, Commands: runnerFunc(fixture.run)}
	return fixture
}

func (fixture *authoredRecoveryFixture) execute(t *testing.T, query string, args ...interface{}) {
	t.Helper()
	if _, err := fixture.database.Exec(query, args...); err != nil {
		t.Fatalf("fixture SQL: %v", err)
	}
}

func (fixture *authoredRecoveryFixture) run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "no-mistakes" {
		return pipelinetest.FixtureCommands{Runner: execx.OSRunner{}}.Run(ctx, request)
	}
	command := strings.Join(request.Args, " ")
	if fixture.nativeFailure == command {
		return execx.Result{ExitCode: 1}, nil
	}
	if command == "axi terminal-preflight --predecessor "+recoveryRun {
		var originalError string
		var active int
		if err := fixture.database.QueryRow("SELECT error FROM runs WHERE id='parent'").Scan(&originalError); err != nil {
			return execx.Result{}, err
		}
		if err := fixture.database.QueryRow("SELECT COUNT(*) FROM runs WHERE id<>? AND status IN ('running','pending')", recoveryRun).Scan(&active); err != nil {
			return execx.Result{}, err
		}
		if originalError != "original Review failure" || active != 0 {
			return execx.Result{ExitCode: 1}, nil
		}
		anchors := map[string]string{}
		for prefix, directory := range map[string]string{"gate/": fixture.bare, "caller/": fixture.caller} {
			result, err := execx.OSRunner{}.Run(ctx, execx.Request{Dir: directory, Name: "git", Args: []string{"for-each-ref", "--format=%(refname) %(objectname)", "refs/no-mistakes/"}})
			if err != nil || result.ExitCode != 0 {
				return result, err
			}
			for _, line := range strings.Split(strings.TrimSpace(string(result.Stdout)), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					anchors[prefix+fields[0]] = fields[1]
				}
			}
		}
		publication := ""
		if fixture.isPublished {
			publication = fixture.parent
		}
		output, err := yaml.Marshal(map[string]interface{}{"predecessor_id": recoveryRun, "repo_id": recoveryRepo, "branch": recoveryBranch, "candidate_sha": fixture.candidate, "candidate_tree": fixture.tree, "caller_is_clean": true, "candidate_is_terminal_head": true, "published_head_sha": publication, "source_anchors": anchors})
		return execx.Result{Stdout: output}, err
	}
	if command != "axi sync --check" && command != "axi sync --recover" {
		return execx.Result{}, fmt.Errorf("unexpected native transport: %s", command)
	}
	var returned int
	if err := fixture.database.QueryRow("SELECT COALESCE(custody_returned_at,0) FROM runs WHERE id=?", recoveryRun).Scan(&returned); err != nil {
		return execx.Result{}, err
	}
	if command == "axi sync --recover" {
		fixture.recoveries++
		if fixture.isPublished {
			if _, err := fixture.database.Exec("UPDATE runs SET custody_returned_at=5,updated_at=6 WHERE id=?", recoveryRun); err != nil {
				return execx.Result{}, err
			}
		}
		if fixture.postMutation != nil {
			fixture.postMutation()
		}
	}
	state, safety, relation, publication := "user_owned", "user_owned", "equal", ""
	if fixture.isPublished {
		state, safety, relation, publication = "pipeline_owned", "blocked_pipeline_owned_recoverable", "ahead", fixture.parent
		if returned != 0 {
			state, safety = "local_ahead", "blocked_local_ahead"
		}
	}
	output := fmt.Sprintf("branch_sync:\n  recovered: %t\n  changed: false\n  state: %s\n  safety: %s\n  relation: %s\n  local: {head: %s, clean: true}\n  pipeline: {run: %s, submitted_head: %s, current_head: %s, pushed_head: '%s'}\n", command == "axi sync --recover", state, safety, relation, fixture.candidate, recoveryRun, fixture.candidate, fixture.candidate, publication)
	if fixture.isPublished && returned == 0 {
		output += "  next_action: {code: recover_custody, command: 'no-mistakes axi sync --recover'}\n"
	}
	return execx.Result{Stdout: []byte(output)}, nil
}

func (fixture *authoredRecoveryFixture) snapshot(t *testing.T) string {
	t.Helper()
	result, err := execx.OSRunner{}.Run(context.Background(), execx.Request{Name: "sqlite3", Args: []string{"-readonly", filepath.Join(fixture.reader.Root, "state.sqlite"), ".dump"}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("fixture dump: %s %v", result.Stderr, err)
	}
	return string(result.Stdout) + runPipelineGit(t, fixture.bare, "show-ref") + runPipelineGit(t, fixture.caller, "show-ref")
}

func TestRecoverAuthoredCandidatePreservesRealSourceAndLedger(t *testing.T) {
	for _, isPublished := range []bool{false, true} {
		t.Run(fmt.Sprintf("published=%t", isPublished), func(t *testing.T) {
			// Arrange: real independent caller/gate repositories and persisted admission.
			fixture := newAuthoredRecoveryFixture(t, isPublished)
			before := fixture.snapshot(t)

			// Act: the production guard and SQL/Git paths run unchanged.
			result, err := fixture.reader.RecoverKeepLocal(context.Background(), fixture.caller, fixture.caller, recoveryBranch, nil)

			// Assert: publication recovery changes only native's custody stamp.
			if err != nil {
				t.Fatal(err)
			}
			wantRecoveries := 0
			if isPublished {
				wantRecoveries = 1
			}
			if result.Head != fixture.candidate || result.GateHead != fixture.parent || fixture.recoveries != wantRecoveries {
				t.Fatalf("wrong source or boundary: %+v recoveries=%d", result, fixture.recoveries)
			}
			var stamp sql.NullInt64
			if err := fixture.database.QueryRow("SELECT custody_returned_at FROM runs WHERE id=?", recoveryRun).Scan(&stamp); err != nil || stamp.Valid != isPublished {
				t.Fatalf("native custody result: %v %v", stamp, err)
			}
			if isPublished {
				fixture.execute(t, "UPDATE runs SET custody_returned_at=NULL,updated_at=3 WHERE id=?", recoveryRun)
			}
			if fixture.snapshot(t) != before {
				t.Fatal("recovery rewrote source, admission, original findings, rounds or repair spending")
			}
			if isPublished {
				fixture.execute(t, "UPDATE runs SET custody_returned_at=5,updated_at=6 WHERE id=?", recoveryRun)
			}
			if _, err := fixture.reader.RecoverKeepLocal(context.Background(), fixture.caller, fixture.caller, recoveryBranch, nil); err != nil {
				t.Fatalf("idempotent supported result: %v", err)
			}
		})
	}
}

func TestRecoverAuthoredCandidateRefusesUnsafeBindings(t *testing.T) {
	for _, isPublished := range []bool{false, true} {
		for _, change := range []string{"active run", "ordinary cancellation", "child publication", "changed generation", "changed PR", "changed launch", "changed intent", "changed source", "unverified source", "uncertain source", "active publisher", "active agent", "repair budget", "repair selection", "predecessor history", "newer run", "other active owner", "missing admission", "corrupt admission", "mismatched admission", "moved gate", "missing authored anchor", "moved authored anchor", "dirty caller", "dirty managed", "foreign caller", "native refusal"} {
			t.Run(fmt.Sprintf("published=%t/%s", isPublished, change), func(t *testing.T) {
				fixture := newAuthoredRecoveryFixture(t, isPublished)
				switch change {
				case "active run":
					fixture.execute(t, "UPDATE runs SET status='running' WHERE id=?", recoveryRun)
				case "ordinary cancellation", "missing admission":
					fixture.execute(t, "DELETE FROM validation_admissions")
				case "child publication":
					fixture.execute(t, "UPDATE runs SET last_pushed_sha=head_sha WHERE id=?", recoveryRun)
				case "changed generation":
					fixture.execute(t, "UPDATE runs SET push_generation=99 WHERE id=?", recoveryRun)
				case "changed PR":
					fixture.execute(t, "UPDATE runs SET pr_url='changed' WHERE id=?", recoveryRun)
				case "changed launch":
					fixture.execute(t, "UPDATE runs SET launch_validation_generation='changed' WHERE id=?", recoveryRun)
				case "changed intent":
					fixture.execute(t, "UPDATE runs SET intent='changed' WHERE id=?", recoveryRun)
				case "changed source":
					fixture.execute(t, "UPDATE runs SET head_sha=? WHERE id=?", fixture.parent, recoveryRun)
				case "unverified source":
					fixture.execute(t, "UPDATE runs SET terminal_head_verified_at=NULL WHERE id=?", recoveryRun)
				case "uncertain source":
					fixture.execute(t, "UPDATE runs SET terminal_source_state='unreadable' WHERE id=?", recoveryRun)
				case "active publisher":
					fixture.execute(t, "UPDATE runs SET push_active=1 WHERE id=?", recoveryRun)
				case "active agent":
					fixture.execute(t, "UPDATE step_results SET agent_pid=? WHERE run_id=?", os.Getpid(), recoveryRun)
				case "repair budget":
					fixture.execute(t, "UPDATE step_results SET auto_fix_limit=1 WHERE run_id=?", recoveryRun)
				case "repair selection":
					fixture.execute(t, "UPDATE step_rounds SET selection_source='user' WHERE id='child-round'")
				case "predecessor history":
					fixture.execute(t, "UPDATE runs SET error='changed' WHERE id='parent'")
				case "newer run", "other active owner":
					created := 0
					if change == "newer run" {
						created = 9
					}
					fixture.execute(t, "INSERT INTO runs(id,repo_id,branch,status,head_sha,submitted_head_sha,created_at) VALUES('another',?,?,'running',?,?,?)", recoveryRepo, recoveryBranch, fixture.candidate, fixture.candidate, created)
				case "corrupt admission":
					fixture.execute(t, "UPDATE validation_admissions SET event_json='unreadable'")
				case "mismatched admission":
					fixture.execute(t, "UPDATE validation_admissions SET event_json=json_set(event_json,'$.InitialChild.LaunchNonce','different')")
				case "moved gate":
					moved := runPipelineGit(t, fixture.caller, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit-tree", fixture.tree, "-p", fixture.candidate, "-m", "unrelated gate movement")
					runPipelineGit(t, fixture.bare, "fetch", "--quiet", fixture.caller, moved)
					runPipelineGit(t, fixture.bare, "update-ref", "refs/heads/"+recoveryBranch, moved)
				case "missing authored anchor":
					runPipelineGit(t, fixture.bare, "update-ref", "-d", "refs/no-mistakes/authored-candidate/admission-one")
				case "moved authored anchor":
					runPipelineGit(t, fixture.bare, "update-ref", "refs/no-mistakes/authored-candidate/admission-one", fixture.parent)
				case "dirty caller":
					writeFixtureFile(t, filepath.Join(fixture.caller, "unfinished.txt"), "unfinished")
				case "dirty managed":
					writeFixtureFile(t, filepath.Join(fixture.managed, "unfinished.txt"), "unfinished")
				case "foreign caller":
					fixture.execute(t, "UPDATE validation_admissions SET event_json=json_set(event_json,'$.Request.caller_dir',?)", t.TempDir())
				case "native refusal":
					fixture.nativeFailure = "axi sync --recover"
					if !isPublished {
						fixture.nativeFailure = "axi sync --check"
					}
				}
				before := fixture.snapshot(t)

				_, err := fixture.reader.RecoverKeepLocal(context.Background(), fixture.caller, fixture.caller, recoveryBranch, nil)

				if err == nil {
					t.Fatal("unsafe recovery accepted")
				}
				if fixture.snapshot(t) != before {
					t.Fatal("refusal changed source, admission or history")
				}
			})
		}
	}
}

// TestRecoverAuthoredCandidateJudgesRecordedAgentByLiveness proves the fix for
// the daemon-crash recovery refusal: a pid left on a failed step by a crash is
// evidence of past ownership, not present ownership, so a step whose process is
// verifiably gone recovers, while a live process, an unreadable liveness, and a
// step still in progress all keep refusing. The naming of the step and pid in a
// live refusal is asserted so a real conflict stays diagnosable.
func TestRecoverAuthoredCandidateJudgesRecordedAgentByLiveness(t *testing.T) {
	for _, isPublished := range []bool{false, true} {
		t.Run(fmt.Sprintf("published=%t", isPublished), func(t *testing.T) {
			for _, scenario := range []struct {
				name, status string
				pid          int
				readState    agentProcessStatus
				isRefused    bool
				wantNamed    bool
			}{
				{name: "failed step whose recorded agent process is gone", status: "failed", pid: deadAgentPID, readState: agentProcessDead},
				{name: "failed step whose recorded agent process is alive", status: "failed", pid: 4242, readState: agentProcessAlive, isRefused: true, wantNamed: true},
				{name: "failed step whose recorded agent liveness is unreadable", status: "failed", pid: 4243, readState: agentProcessUnknown, isRefused: true, wantNamed: true},
				{name: "in-progress step even with a dead recorded pid", status: "running", pid: deadAgentPID, readState: agentProcessDead, isRefused: true},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					fixture := newAuthoredRecoveryFixture(t, isPublished)
					// The recorded shape of issue 2166 is a failed `test` step whose
					// interrupted agent pid is gone and whose repair budget is zero.
					fixture.execute(t, "UPDATE step_results SET step_name='test',status=?,agent_pid=? WHERE id='child-review'", scenario.status, scenario.pid)
					previous := agentProcessReader
					agentProcessReader = func(int) agentProcessStatus { return scenario.readState }
					t.Cleanup(func() { agentProcessReader = previous })

					_, err := fixture.reader.RecoverKeepLocal(context.Background(), fixture.caller, fixture.caller, recoveryBranch, nil)

					if scenario.isRefused {
						if err == nil {
							t.Fatal("recovery accepted a step the run may still own")
						}
						if scenario.wantNamed && (!strings.Contains(err.Error(), "step test") || !strings.Contains(err.Error(), strconv.Itoa(scenario.pid))) {
							t.Fatalf("refusal does not name the step and pid: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("recovery refused a step whose recorded agent process is verifiably gone: %v", err)
					}
				})
			}
		})
	}
}

func TestRecoverAuthoredCandidateRejectsChangesDuringNativeReturn(t *testing.T) {
	for _, change := range []string{"gate", "anchor", "admission"} {
		t.Run(change, func(t *testing.T) {
			fixture := newAuthoredRecoveryFixture(t, true)
			fixture.postMutation = func() {
				switch change {
				case "gate":
					runPipelineGit(t, fixture.bare, "update-ref", "refs/heads/"+recoveryBranch, fixture.candidate)
				case "anchor":
					runPipelineGit(t, fixture.bare, "update-ref", "refs/no-mistakes/authored-candidate/admission-one", fixture.parent)
				case "admission":
					fixture.execute(t, "UPDATE validation_admissions SET event_json=json_set(event_json,'$.InitialChild.LaunchNonce','different')")
				}
			}
			_, err := fixture.reader.RecoverKeepLocal(context.Background(), fixture.caller, fixture.caller, recoveryBranch, nil)
			if err == nil {
				t.Fatal("native return race accepted")
			}
		})
	}
}
