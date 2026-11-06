package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
)

func TestAdmissionBindsControllerSourceWithoutEditingHistory(t *testing.T) {
	for _, scenario := range []string{"authored", "terminal", "wrong project", "missing predecessor", "wrong repository", "wrong branch", "foreign caller", "wrong head", "wrong tree", "abbreviated candidate", "dirty tracked", "dirty untracked", "dirty submodule", "git read fails", "wrong policy hash", "wrong policy class", "renewed review budget", "claims historical completeness", "missing terminal acceptance", "reused terminal generation", "wrong command", "unknown field", "trailing JSON", "non UTF-8", "oversized", "unreadable", "native refusal", "native unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange. This persisted fixture is independent of the driver's query.
			root, worktree := t.TempDir(), t.TempDir()
			caller, err := fsx.Canonical(worktree)
			if err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(root, "registered-project")
			database := filepath.Join(root, "state.sqlite")
			writer, err := sql.Open("sqlite", database)
			if err != nil {
				t.Fatal(err)
			}
			_, err = writer.Exec(`CREATE TABLE repos(id TEXT,working_path TEXT); CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT); INSERT INTO repos VALUES('repo-1',?); INSERT INTO runs VALUES('predecessor','repo-1','fix/admission');`, project)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(database)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := testPolicy(t).Select("high-risk")
			if err != nil {
				t.Fatal(err)
			}
			policy := map[string]interface{}{"sha256": selection.Hash, "class": "high-risk", "review_cycles": 3}
			payload := map[string]interface{}{
				"predecessor_id": "predecessor", "repo_id": "repo-1", "branch": "fix/admission", "caller_dir": caller,
				"candidate_sha": strings.Repeat("a", 40), "candidate_tree": strings.Repeat("b", 40),
				"snapshot_digest": strings.Repeat("c", 64), "intent_digest": strings.Repeat("d", 64),
				"launch_nonce": "original-nonce", "validation_generation": "original-generation",
				"controller_policy": policy, "historical_source_completeness": "unknown", "reason": "Validate this committed candidate only.",
				"launch_assertion":   map[string]interface{}{"apply": true},
				"published_head_sha": "", "push_generation": 0, "push_target_fingerprint": "",
			}
			operation := "validate-authored"
			isTerminal := scenario == "terminal" || scenario == "missing terminal acceptance" || scenario == "reused terminal generation"
			if isTerminal {
				operation = "validate-terminal"
				payload["terminal_generation"] = "independent-generation"
				payload["accept_unknown_source"] = true
				payload["source_anchors"] = map[string]string{"caller/refs/no-mistakes/recover/predecessor": strings.Repeat("e", 40)}
			}
			switch scenario {
			case "wrong project":
				project = root
			case "missing predecessor":
				payload["predecessor_id"] = "missing"
			case "wrong repository":
				payload["repo_id"] = "repo-2"
			case "wrong branch":
				payload["branch"] = "fix/foreign"
			case "foreign caller":
				payload["caller_dir"] = root
			case "wrong head":
				payload["candidate_sha"] = strings.Repeat("e", 40)
			case "wrong tree":
				payload["candidate_tree"] = strings.Repeat("e", 40)
			case "abbreviated candidate":
				payload["candidate_sha"] = "abc123"
			case "wrong policy hash":
				policy["sha256"] = strings.Repeat("e", 64)
			case "wrong policy class":
				policy["class"] = "ordinary"
			case "renewed review budget":
				policy["review_cycles"] = 4
			case "claims historical completeness":
				payload["historical_source_completeness"] = "complete"
			case "missing terminal acceptance":
				payload["accept_unknown_source"] = false
			case "reused terminal generation":
				payload["terminal_generation"] = "original-generation"
			case "wrong command":
				payload["terminal_generation"] = "independent-generation"
			case "unknown field":
				payload["repairs"] = 1
			}
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "trailing JSON":
				data = append(data, []byte(" {}")...)
			case "non UTF-8":
				data = append(data, 0xff)
			case "oversized":
				data = bytes.Repeat([]byte(" "), 65537)
			}
			path := filepath.Join(root, "admission.json")
			if scenario != "unreadable" {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			nativeCalls := 0
			var snapshotPath string
			commands := gatedb.Runner{Next: runnerFunc(func(_ context.Context, request execx.Request) (execx.Result, error) {
				if request.Name == "git" {
					switch strings.Join(request.Args, " ") {
					case "symbolic-ref --quiet --short HEAD":
						return execx.Result{Stdout: []byte("fix/admission")}, nil
					case "rev-parse HEAD":
						return execx.Result{Stdout: []byte(strings.Repeat("a", 40))}, nil
					case "rev-parse HEAD^{tree}":
						return execx.Result{Stdout: []byte(strings.Repeat("b", 40))}, nil
					case "status --porcelain=v1 --untracked-files=all --ignore-submodules=none":
						switch scenario {
						case "dirty tracked":
							return execx.Result{Stdout: []byte(" M source.go")}, nil
						case "dirty untracked":
							return execx.Result{Stdout: []byte("?? missing.go")}, nil
						case "dirty submodule":
							return execx.Result{Stdout: []byte(" m dependency")}, nil
						case "git read fails":
							return execx.Result{}, errors.New("checkout unavailable")
						}
						return execx.Result{}, nil
					}
				}
				if request.Name != "no-mistakes" || len(request.Args) != 4 || request.Args[0] != "axi" || request.Args[1] != operation || request.Args[2] != "--admission-file" || request.Dir != caller {
					t.Fatalf("unexpected command %s %q in %q", request.Name, request.Args, request.Dir)
				}
				nativeCalls++
				snapshotPath = request.Args[3]
				got, err := os.ReadFile(snapshotPath)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("admission bytes changed: %v", err)
				}
				if scenario == "native refusal" {
					return execx.Result{ExitCode: 2, Stderr: []byte("immutable snapshot changed")}, nil
				}
				if scenario == "native unavailable" {
					return execx.Result{}, errors.New("native missing")
				}
				return execx.Result{Stdout: []byte("native exact child receipt and cumulative spending")}, nil
			})}
			reader := Reader{Root: root, Commands: commands}

			// Act
			result, digest, err := reader.Admit(t.Context(), project, caller, "fix/admission", operation, path, selection, []string{"NM_HOME=" + root})

			// Assert
			shouldInvoke := scenario == "authored" || scenario == "terminal" || scenario == "native refusal" || scenario == "native unavailable"
			if shouldInvoke {
				if nativeCalls != 1 || len(digest) != 64 {
					t.Fatalf("calls=%d digest length=%d err=%v", nativeCalls, len(digest), err)
				}
				if scenario == "native unavailable" {
					if err == nil || !strings.Contains(err.Error(), "native missing") {
						t.Fatalf("native error lost: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if scenario == "native refusal" && (result.ExitCode != 2 || string(result.Stderr) != "immutable snapshot changed") {
					t.Fatal("native refusal changed")
				}
			} else if err == nil || nativeCalls != 0 || digest != "" {
				t.Fatalf("unsafe admission: calls=%d err=%v", nativeCalls, err)
			}
			after, err := os.ReadFile(database)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("predecessor database changed: %v", err)
			}
			if snapshotPath != "" {
				if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
					t.Fatalf("snapshot remains: %v", err)
				}
			}
		})
	}
}

func TestAdmissionBranchRefusesInvalidIdentityBeforeQuery(t *testing.T) {
	reader := Reader{Root: t.TempDir(), Commands: runnerFunc(func(context.Context, execx.Request) (execx.Result, error) {
		return execx.Result{}, fmt.Errorf("unexpected query")
	})}
	_, _, err := reader.AdmissionBranch(t.Context(), t.TempDir(), "../foreign")
	if err == nil || !strings.Contains(err.Error(), "invalid admission predecessor identity") {
		t.Fatalf("identity refusal = %v", err)
	}
}
