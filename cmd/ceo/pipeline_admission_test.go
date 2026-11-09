package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

type admissionCommands struct {
	worktree, branch, admission string
	native                      []execx.Request
	result                      execx.Result
	nativeErr                   error
}

func admissionControllerStage(t *testing.T) (home.Home, string, *pipelineRunner) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the primary CEO registration is verified on Windows only")
	}
	return registeredCEOStage(t, "ceo", pipeline.Gate{})
}

func (commands *admissionCommands) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch request.Name {
	case "git":
		switch strings.Join(request.Args, " ") {
		case "rev-parse --show-toplevel":
			return execx.Result{Stdout: []byte(request.Dir)}, nil
		case "symbolic-ref --quiet --short HEAD":
			return execx.Result{Stdout: []byte(commands.branch)}, nil
		case "rev-parse HEAD":
			return execx.Result{Stdout: []byte(strings.Repeat("b", 40))}, nil
		case "rev-parse HEAD^{tree}":
			return execx.Result{Stdout: []byte(strings.Repeat("c", 40))}, nil
		case "status --porcelain=v1 --untracked-files=all --ignore-submodules=none":
			return execx.Result{}, nil
		}
	case "sqlite3":
		return execx.Result{Stdout: []byte(fmt.Sprintf(`[{"repo_id":"repo-1","branch":%q,"id":"predecessor"}]`, commands.branch))}, nil
	case "no-mistakes":
		commands.native = append(commands.native, request)
		if len(request.Args) == 4 && request.Args[0] == "axi" && request.Args[2] == "--admission-file" {
			data, err := os.ReadFile(request.Args[3])
			if err != nil || string(data) != commands.admission {
				return execx.Result{}, fmt.Errorf("native did not receive the exact controller admission: %v", err)
			}
			return commands.result, commands.nativeErr
		}
		if reflect.DeepEqual(request.Args, []string{"axi", "terminal-preflight", "--predecessor", "predecessor"}) {
			return commands.result, commands.nativeErr
		}
	}
	return execx.Result{}, fmt.Errorf("unexpected admission command: %s %v", request.Name, request.Args)
}

func TestPipelineAdmissionForwardsExactControllerFile(t *testing.T) {
	for _, scenario := range []struct {
		name, suffix string
	}{
		{name: "registered path"},
		{name: "equivalent path", suffix: string(os.PathSeparator) + "."},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			h, root, fixture := admissionControllerStage(t)
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			if scenario.suffix != "" {
				meta.Worktree += scenario.suffix
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
			}
			caller, err := fsx.Canonical(meta.Worktree)
			if err != nil {
				t.Fatal(err)
			}
			data := controllerAdmission(t, meta, false)
			file := filepath.Join(h.Root, "admission.json")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			commands := &admissionCommands{worktree: fixture.worktree, branch: "feat/admission", admission: string(data), result: execx.Result{Stdout: []byte("authored_validation:\n  automatic_repairs_remaining: 0\n  manual_repairs_remaining: 0\n  spending: native-ledger\n")}}
			var output bytes.Buffer

			// Act
			err = pipelineCommand(t.Context(), h, root, commands, []string{"validate-authored", "task", "--admission-file", file}, &output)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(commands.native) != 1 {
				t.Fatalf("native calls = %d, want one admission", len(commands.native))
			}
			request := commands.native[0]
			if len(request.Args) != 4 || !reflect.DeepEqual(request.Args[:3], []string{"axi", "validate-authored", "--admission-file"}) || request.Dir != caller {
				t.Fatalf("native args = %q in %q", request.Args, request.Dir)
			}
			if request.Args[3] == file {
				t.Fatal("native should receive a private byte-identical snapshot of the verified file")
			}
			if _, err := os.Stat(request.Args[3]); !os.IsNotExist(err) {
				t.Fatalf("private admission snapshot survived invocation: %v", err)
			}
			if output.String() != string(commands.result.Stdout) {
				t.Fatalf("native result changed: %q", output.String())
			}
		})
	}
}

func controllerAdmission(t *testing.T, meta state.TaskMeta, isTerminal bool) []byte {
	t.Helper()
	selection, err := pipeline.LoadSelection(filepath.Join(meta.TaskTmp, "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := fsx.Canonical(meta.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	admission := map[string]interface{}{
		"predecessor_id": "predecessor", "repo_id": "repo-1", "branch": "feat/admission", "caller_dir": caller,
		"candidate_sha": strings.Repeat("b", 40), "candidate_tree": strings.Repeat("c", 40),
		"snapshot_digest": strings.Repeat("d", 64), "intent_digest": strings.Repeat("e", 64),
		"launch_nonce": "original-nonce", "validation_generation": "original-generation",
		"published_head_sha": strings.Repeat("a", 40), "push_generation": 7, "push_target_fingerprint": "original-publication",
		"controller_policy":              map[string]interface{}{"sha256": selection.Hash, "class": selection.Class, "review_cycles": selection.ReviewCycles},
		"launch_assertion":               map[string]interface{}{"trusted_sha": strings.Repeat("f", 40), "apply": true, "profiles": map[string]interface{}{}},
		"historical_source_completeness": "unknown", "reason": "Validate the declared authored correction without renewed repairs.",
	}
	if isTerminal {
		admission["terminal_generation"] = "independent-generation"
		admission["accept_unknown_source"] = true
		admission["source_anchors"] = map[string]string{"caller/refs/no-mistakes/recover/predecessor": strings.Repeat("a", 40)}
	}
	data, err := json.Marshal(admission)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPipelineAdmissionRetainsNativeResultsWithoutFallback(t *testing.T) {
	for _, scenario := range []string{"terminal", "observed progressed child", "observed cancelled child", "completed ordinary predecessor", "unsupported native", "hold elapsed", "native error", "unwritable result"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			h, root, fixture := admissionControllerStage(t)
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			isTerminal := scenario == "terminal"
			operation := "validate-authored"
			if isTerminal {
				operation = "validate-terminal"
			}
			data := controllerAdmission(t, meta, isTerminal)
			file := filepath.Join(h.Root, "admission.json")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			commands := &admissionCommands{worktree: fixture.worktree, branch: "feat/admission", admission: string(data), result: execx.Result{Stdout: []byte("launch_receipt:\n  run_id: same-child\nauthored_validation:\n  spending: exact-native-ledger\n  automatic_repairs_remaining: 0\n  manual_repairs_remaining: 0\n")}}
			var wantError string
			switch scenario {
			case "completed ordinary predecessor":
				commands.result.ExitCode = 1
				commands.result.Stderr = []byte("authored admission refused: completed ordinary predecessors are ineligible")
				wantError = "exited 1"
			case "observed cancelled child":
				commands.result.ExitCode = 1
				commands.result.Stdout = append(commands.result.Stdout, []byte("outcome: cancelled\n")...)
				wantError = "exited 1"
			case "unsupported native":
				commands.result.ExitCode = 2
				commands.result.Stderr = []byte("unknown command validate-authored")
				wantError = "--admission-file contract is required"
			case "hold elapsed":
				commands.result.ExitCode = 1
				commands.result.Stderr = []byte("elapsed while driving the run")
				wantError = "native admission hold elapsed"
			case "native error":
				commands.nativeErr = errors.New("native execution unavailable")
				wantError = "native execution unavailable"
			case "unwritable result":
				if err := os.Mkdir(filepath.Join(meta.TaskTmp, "validation-admissions.jsonl"), 0o700); err != nil {
					t.Fatal(err)
				}
				wantError = "could not retain native admission result"
			}
			var output bytes.Buffer
			attempts := 1
			if strings.HasPrefix(scenario, "observed") {
				attempts = 2
			}

			// Act
			for attempt := 0; attempt < attempts; attempt++ {
				output.Reset()
				err = pipelineCommand(t.Context(), h, root, commands, []string{operation, "task", "--admission-file", file}, &output)
				if wantError == "" && err != nil {
					t.Fatal(err)
				}
				if wantError != "" && (err == nil || !strings.Contains(err.Error(), wantError)) {
					t.Fatalf("err=%v want %q", err, wantError)
				}
				if scenario == "hold elapsed" && !errors.Is(err, pipeline.ErrUnresolved) {
					t.Fatalf("hold was misreported: %v", err)
				}
			}

			// Assert
			if len(commands.native) != attempts {
				t.Fatalf("native calls=%d want %d", len(commands.native), attempts)
			}
			if output.String() != string(commands.result.Stdout)+string(commands.result.Stderr) {
				t.Fatal("native result was rewritten")
			}
			for _, request := range commands.native {
				if request.Args[1] != operation {
					t.Fatalf("fallback to %q", request.Args)
				}
			}
			if scenario == "unwritable result" {
				return
			}
			journal, err := os.ReadFile(filepath.Join(meta.TaskTmp, "validation-admissions.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			rows := bytes.Split(bytes.TrimSpace(journal), []byte("\n"))
			if len(rows) != attempts {
				t.Fatalf("retained %d results, want %d", len(rows), attempts)
			}
			for _, row := range rows {
				var result struct {
					Stdout, Stderr, Error string
					ExitCode              int    `json:"exit_code"`
					Digest                string `json:"admission_sha256"`
				}
				if err := json.Unmarshal(row, &result); err != nil {
					t.Fatal(err)
				}
				if result.Stdout != string(commands.result.Stdout) || result.Stderr != string(commands.result.Stderr) || result.ExitCode != commands.result.ExitCode || len(result.Digest) != 64 {
					t.Fatal("retained result lost native proof or spending")
				}
				if commands.nativeErr != nil && result.Error != commands.nativeErr.Error() {
					t.Fatal("execution error was lost")
				}
			}
		})
	}
}

func TestPipelineAdmissionRefusesNonControllerAndMigration(t *testing.T) {
	for _, scenario := range []string{"unregistered caller", "pending migration", "unreadable migration", "missing file", "extra repair flag", "foreign branch"} {
		t.Run(scenario, func(t *testing.T) {
			var h home.Home
			var root string
			if scenario == "unregistered caller" {
				h, root, _ = extraWorktreeTask(t)
			} else {
				h, root, _ = admissionControllerStage(t)
			}
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"validate-authored", "task", "--admission-file", filepath.Join(h.Root, "missing.json")}
			want := "read admission"
			switch scenario {
			case "unregistered caller":
				want = "registered primary CEO"
			case "pending migration":
				if err := os.WriteFile(filepath.Join(meta.TaskTmp, policyMigrationJournalName), []byte("invalid pending migration"), 0o600); err != nil {
					t.Fatal(err)
				}
				want = "pending policy migration"
			case "unreadable migration":
				if err := os.Mkdir(filepath.Join(meta.TaskTmp, policyMigrationJournalName), 0o700); err != nil {
					t.Fatal(err)
				}
				want = "pending policy migration"
			case "extra repair flag":
				args = append(args, "--yes")
				want = "flag provided but not defined"
			case "foreign branch":
				args = append(args, "--branch", "fix/foreign")
				want = "checked out in none"
			}
			before, err := os.ReadFile(filepath.Join(meta.TaskTmp, "pipeline.json"))
			if err != nil {
				t.Fatal(err)
			}
			commands := &admissionCommands{branch: "feat/admission"}

			err = pipelineCommand(t.Context(), h, root, commands, args, &bytes.Buffer{})

			if err == nil || !strings.Contains(err.Error(), want) || len(commands.native) != 0 {
				t.Fatalf("refusal=%v native calls=%d want %q", err, len(commands.native), want)
			}
			after, err := os.ReadFile(filepath.Join(meta.TaskTmp, "pipeline.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("frozen policy changed: %v", err)
			}
			if lines, err := state.TailStatus(h.State, "task", 10); err != nil || len(lines) != 0 {
				t.Fatalf("refusal mutated task audit: %v", err)
			}
		})
	}
}

func TestPipelineTerminalPreflightRemainsReadOnly(t *testing.T) {
	h, root, fixture := admissionControllerStage(t)
	commands := &admissionCommands{worktree: fixture.worktree, branch: "feat/admission", result: execx.Result{Stdout: []byte("caller_is_clean: false\nhistorical_source_completeness: unknown\nhas_competing_run: true\n")}}
	var output bytes.Buffer

	err := pipelineCommand(t.Context(), h, root, commands, []string{"terminal-preflight", "task", "--run", "predecessor"}, &output)

	if err != nil || output.String() != string(commands.result.Stdout) || len(commands.native) != 1 {
		t.Fatalf("preflight err=%v native calls=%d", err, len(commands.native))
	}
	if lines, err := state.TailStatus(h.State, "task", 10); err != nil || len(lines) != 0 {
		t.Fatalf("preflight wrote audit: %v", err)
	}
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(meta.TaskTmp, "validation-admissions.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("preflight recorded an admission: %v", err)
	}
}

func TestPipelineAdmissionAndPreflightEnforceCallerRole(t *testing.T) {
	for _, operation := range []string{"validate-authored", "validate-terminal", "terminal-preflight"} {
		for _, scenario := range []struct {
			name, terminal, gate string
			isInvalidProof       bool
		}{
			{name: "registered CEO"},
			{name: "gate inherits CEO proof", gate: "1"},
			{name: "gate inherits own contributor proof", terminal: "task", gate: "1"},
			{name: "gate inherits foreign contributor proof", terminal: "another-task", gate: "1"},
			{name: "own contributor inherits CEO ancestry", terminal: "task"},
			{name: "foreign contributor inherits CEO ancestry", terminal: "another-task"},
			{name: "invalid contributor proof inherits CEO ancestry", terminal: "task", isInvalidProof: true},
		} {
			t.Run(operation+"/"+scenario.name, func(t *testing.T) {
				h, root, fixture := admissionControllerStage(t)
				inGoblinTerminal(t, h.State, "ceo")
				t.Setenv(harness.RoleVariable, "ceo")
				record, err := host.ReadRecord(h.State, "ceo")
				if err != nil {
					t.Fatal(err)
				}
				var isStarted bool
				record.ChildStart, isStarted = proc.StartTime(os.Getpid())
				if !isStarted {
					t.Fatal("the registered CEO process has no start time")
				}
				record.ProofSum = host.ProofSum(os.Getenv(host.ProofVariable))
				data, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.State, "hosts", "ceo.json"), data, 0o600); err != nil {
					t.Fatal(err)
				}
				if scenario.terminal != "" {
					inGoblinTerminal(t, h.State, scenario.terminal)
				}
				if scenario.isInvalidProof {
					t.Setenv(host.ProofVariable, "unissued-proof")
				}
				t.Setenv(gateAgentVariable, scenario.gate)
				ceo := supervisor.CEOConnection{State: h.State}
				_, release, err := ceo.CallerIdentity()
				if err != nil {
					t.Fatalf("fixture must carry inherited CEO identity: %v", err)
				}
				release()
				meta, err := state.ReadTaskMeta(h.State, "task")
				if err != nil {
					t.Fatal(err)
				}
				data = controllerAdmission(t, meta, operation == "validate-terminal")
				file := filepath.Join(h.Root, "admission.json")
				if err := os.WriteFile(file, data, 0o600); err != nil {
					t.Fatal(err)
				}
				commands := &admissionCommands{worktree: fixture.worktree, branch: "feat/admission", admission: string(data), result: execx.Result{Stdout: []byte("private native result")}}
				args := []string{operation, "task", "--admission-file", file}
				if operation == "terminal-preflight" {
					args = []string{operation, "task", "--run", "predecessor"}
				}
				var output bytes.Buffer

				err = pipelineCommand(t.Context(), h, root, commands, args, &output)

				isAllowed := scenario.gate == "" && (scenario.terminal == "" || (operation == "terminal-preflight" && scenario.terminal == "task" && !scenario.isInvalidProof))
				if isAllowed {
					if err != nil || len(commands.native) != 1 || output.String() != "private native result" {
						t.Fatalf("authorized caller: %v, native calls=%d, output=%q", err, len(commands.native), output.String())
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), "registered primary CEO") || len(commands.native) != 0 || output.Len() != 0 {
					t.Fatalf("unauthorized caller: %v, native calls=%d, output=%q", err, len(commands.native), output.String())
				}
				if lines, err := state.TailStatus(h.State, "task", 10); err != nil || len(lines) != 0 {
					t.Fatalf("unauthorized caller wrote task audit: %v", err)
				}
				if _, err := os.Stat(filepath.Join(meta.TaskTmp, "validation-admissions.jsonl")); !os.IsNotExist(err) {
					t.Fatalf("unauthorized caller recorded an admission: %v", err)
				}
			})
		}
	}
}

func TestPipelineTerminalPreflightRefusesAnUnrelatedCaller(t *testing.T) {
	h, root, _ := extraWorktreeTask(t)
	commands := &admissionCommands{branch: "feat/admission"}
	err := pipelineCommand(t.Context(), h, root, commands, []string{"terminal-preflight", "task", "--run", "predecessor"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "task's own goblin") || len(commands.native) != 0 {
		t.Fatalf("unrelated caller: %v, native calls=%d", err, len(commands.native))
	}
}

func TestPipelineTerminalPreflightAllowsOnlyItsOwnGoblin(t *testing.T) {
	for _, task := range []string{"task", "another-task"} {
		t.Run(task, func(t *testing.T) {
			h, root, branches := extraWorktreeTask(t)
			inGoblinTerminal(t, h.State, task)
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			commands := &admissionCommands{worktree: meta.Worktree, branch: branches[meta.Worktree]}

			err = pipelineCommand(t.Context(), h, root, commands, []string{"terminal-preflight", "task", "--run", "predecessor"}, &bytes.Buffer{})

			if task == "task" {
				if err != nil || len(commands.native) != 1 {
					t.Fatalf("own preflight: %v, calls=%d", err, len(commands.native))
				}
			} else if err == nil || len(commands.native) != 0 {
				t.Fatalf("foreign preflight: %v, calls=%d", err, len(commands.native))
			}
		})
	}
}

func TestPipelineAdmissionPublicDispatchAndRequiredArguments(t *testing.T) {
	for _, operation := range []string{"validate-authored", "validate-terminal", "terminal-preflight"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("CEO_HOME", t.TempDir())
			t.Setenv("CEO_STATE_OVERRIDE", "")
			t.Setenv("NM_HOME", t.TempDir())
			var stdout, stderr bytes.Buffer

			runtime := commandRuntime{resolveHome: func() (home.Home, error) { return home.Home{Root: t.TempDir(), State: t.TempDir()}, nil }}
			exit := runPipeline([]string{operation, "task"}, &stdout, &stderr, runtime)

			want := "--admission-file is required"
			if operation == "terminal-preflight" {
				want = "terminal-preflight requires --run"
			}
			if exit != 1 || !strings.Contains(stderr.String(), want) {
				t.Fatalf("dispatch exit=%d, stderr=%q", exit, stderr.String())
			}
		})
	}
}
