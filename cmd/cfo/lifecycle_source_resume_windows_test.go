package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

type sourceResumeCommands struct {
	native     []execx.Request
	queryError error
}

type sourceResumeEvent struct {
	Kind   string   `json:"kind"`
	Args   []string `json:"args,omitempty"`
	Prompt string   `json:"prompt,omitempty"`
}

func runSourceResumeTestProgram() (int, bool) {
	if len(os.Args) > 1 && os.Args[1] == "source-resume-host" {
		return runHost(os.Args[2:], os.Stderr), true
	}
	if index := slices.Index(os.Args, "--source-resume-harness"); index > 0 {
		if len(os.Args) != index+3 {
			return 2, true
		}
		return runSourceResumeHarness(os.Args[index+1], os.Args[index+2]), true
	}
	return 0, false
}

func runSourceResumeHarness(recordPath, instructionPath string) int {
	file, err := os.OpenFile(recordPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	defer file.Close()
	record := json.NewEncoder(file)
	if err := record.Encode(sourceResumeEvent{Kind: "launch", Args: os.Args[1:]}); err != nil {
		return 2
	}
	if err := windows.SetConsoleMode(windows.Handle(os.Stdin.Fd()), windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		return 2
	}
	draw := func(text string) {
		fmt.Print("\x1b[2J\x1b[H\r\n\u203a " + text + "\r\n100% context left")
	}
	draw("Ask Codex to do anything")
	reader := bufio.NewReader(os.Stdin)
	var line strings.Builder
	for {
		character, _, err := reader.ReadRune()
		if err != nil {
			return 0
		}
		if character == '\x1b' {
			for {
				next, _, err := reader.ReadRune()
				if err != nil {
					return 2
				}
				if next >= '@' && next <= '~' && next != '[' {
					break
				}
			}
			continue
		}
		if character == '\r' {
			if line.String() != "Read "+instructionPath+" and follow it exactly: it is your instruction from the CEO." {
				return 2
			}
			prompt, err := os.ReadFile(instructionPath)
			if err != nil || record.Encode(sourceResumeEvent{Kind: "prompt", Prompt: string(prompt)}) != nil {
				return 2
			}
			fmt.Print("\x1b[2J\x1b[H\u2022 Working (0s \u2022 esc to interrupt)\r\n\u203a Ask Codex to do anything\r\n100% context left")
			line.Reset()
			continue
		}
		line.WriteRune(character)
		draw(line.String())
	}
}

type sourceResumeAdapter struct {
	programPath     string
	recordPath      string
	instructionPath string
	isRefused       bool
}

func (sourceResumeAdapter) Kind() harness.Kind { return harness.Codex }

func (sourceResumeAdapter) Validate(context.Context, execx.Runner) error { return nil }

func (sourceResumeAdapter) Control() harness.Control {
	return harness.Control{ResumeArgs: []string{"resume"}}
}

func (adapter sourceResumeAdapter) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	if adapter.isRefused {
		return harness.Launch{}, errors.New("fixture provider refused this session")
	}
	return harness.Launch{
		Executable: adapter.programPath,
		Args:       []string{"--source-resume-harness", adapter.recordPath, adapter.instructionPath},
		Env: map[string]string{
			"TEMP": spec.Scratch, "TMP": spec.Scratch, "TMPDIR": spec.Scratch, "GOTMPDIR": spec.Scratch,
		},
		PromptFile: spec.BriefPath,
	}, nil
}

type sourceResumeAuth struct{}

func (sourceResumeAuth) Preflight(context.Context, string, auth.Need) (auth.Result, error) {
	return auth.Result{}, nil
}

func sourceResumeEnvironment(t *testing.T, root string) []string {
	t.Helper()
	var toolDirectories []string
	for _, name := range []string{"git", "sqlite3"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("%s is required: %v", name, err)
		}
		toolDirectories = append(toolDirectories, filepath.Dir(path))
	}
	toolDirectories = append(toolDirectories, filepath.Join(os.Getenv("SystemRoot"), "System32"))
	environment := []string{
		"SystemRoot=" + os.Getenv("SystemRoot"), "WINDIR=" + os.Getenv("SystemRoot"),
		"ComSpec=" + os.Getenv("ComSpec"), "PATHEXT=.COM;.EXE;.BAT;.CMD",
		"PATH=" + strings.Join(toolDirectories, string(os.PathListSeparator)),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=NUL", "GIT_TERMINAL_PROMPT=0",
		"GITHUB_TOKEN=fixture-credential", "CFO_SOURCE_RESUME_RUNNER=1",
	}
	for _, entry := range []struct{ name, folder string }{
		{"USERPROFILE", "profile"}, {"HOME", "profile"}, {"APPDATA", "appdata"}, {"LOCALAPPDATA", "cache"},
		{"TEMP", "temp"}, {"TMP", "temp"}, {"TMPDIR", "temp"}, {"GOTMPDIR", "temp"},
		{"CFO_HOME", "home"}, {"CFO_STATE_OVERRIDE", "home/state"}, {"CFO_PROJECTS_ROOT", "projects"},
		{"CFO_VERIFY_DIR", "verify"}, {"CFO_CODEX_GUARD", "guard"}, {"XDG_CACHE_HOME", "cache"},
		{"CODEX_HOME", "codex"}, {"CLAUDE_CONFIG_DIR", "claude"}, {"PI_CODING_AGENT_DIR", "pi"},
	} {
		path := filepath.Join(root, filepath.FromSlash(entry.folder))
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		environment = append(environment, entry.name+"="+path)
	}
	return environment
}

// The acceptance oracle is source-only resume: one contributor, the same
// original validation history and policy, and no native start/recover/respond.
// OS environment, auth, admission and provider inputs are mocked; Switch,
// Launch, RunArgs, the terminal and prompt delivery execute together.
func TestSourceResumeComposedBoundaries(t *testing.T) {
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CFO_SOURCE_RESUME_RUNNER") != "1" {
		root, err := os.MkdirTemp("", "sr-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		})
		bin := filepath.Join(root, "bin")
		if err := os.MkdirAll(bin, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"no-mistakes.exe", "gh.exe"} {
			standin.Put(t, filepath.Join(bin, name))
		}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, program, "-test.v", "-test.timeout=15m", "-test.run=^TestSourceResumeComposedBoundaries$")
		command.Env = append(sourceResumeEnvironment(t, root), "CFO_SOURCE_RESUME_ROOT="+root)
		output, err := command.CombinedOutput()
		t.Logf("OS/auth/admission/provider inputs mocked; composed boundary evidence:\n%s", output)
		if err != nil {
			t.Fatalf("isolated seven-case runner: %v", err)
		}
		return
	}
	runnerRoot := os.Getenv("CFO_SOURCE_RESUME_ROOT")
	if runnerRoot == "" {
		t.Fatal("isolated runner root is required")
	}
	for index, test := range []struct {
		name, refusal string
	}{
		{name: "original relaunch once and replay"},
		{name: "original finishes during memory pause"},
		{name: "foreign same-intent run", refusal: "another validation run owns"},
		{name: "unreadable state", refusal: "state query failed"},
		{name: "low memory", refusal: "Resume needs at least 5 GB"},
		{name: "exhausted allowance", refusal: "fixture provider allowance exhausted"},
		{name: "provider refusal through Switch", refusal: "fixture provider refused this session"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := filepath.Join(runnerRoot, fmt.Sprint(index))
			environment := sourceResumeEnvironment(t, root)
			nativeRecord := filepath.Join(root, "native-calls.txt")
			environment = append(environment, standin.Env(nativeRecord,
				standin.Rule{Args: "pr list", Prefix: true, Stdout: "[]"},
				standin.Rule{Any: true, Exit: 1, Stderr: "validation must be requested explicitly"})...)
			for _, entry := range environment {
				name, value, _ := strings.Cut(entry, "=")
				t.Setenv(name, value)
			}
			bin := filepath.Join(runnerRoot, "bin")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			for _, name := range []string{"no-mistakes", "gh"} {
				if path, err := exec.LookPath(name); err != nil || !strings.EqualFold(path, filepath.Join(bin, name+".exe")) {
					t.Fatalf("%s did not resolve to the pinned fake: %q, %v", name, path, err)
				}
			}
			h := home.Home{Root: filepath.Join(root, "home"), State: filepath.Join(root, "home", "state"), Data: filepath.Join(root, "home", "data")}
			for name, content := range map[string]string{"AGENTS.md": "# fixture", home.InstalledMarker: ""} {
				if err := os.WriteFile(filepath.Join(h.Root, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("CFO_HOME", h.Root)
			t.Setenv("CFO_STATE_OVERRIDE", h.State)
			nativeRoot := filepath.Join(root, "native")
			if err := os.MkdirAll(nativeRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NM_HOME", nativeRoot)
			commands := execx.OSRunner{}
			workingDir := h.Root
			git := func(args ...string) string {
				t.Helper()
				result, err := commands.Run(t.Context(), execx.Request{Dir: workingDir, Name: "git", Args: args})
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("git %v: %v %s", args, err, result.Stderr)
				}
				return strings.TrimSpace(string(result.Stdout))
			}
			git("init")
			git("config", "user.email", "t@t")
			git("config", "user.name", "t")
			git("add", "AGENTS.md")
			git("commit", "-m", "fixture source")
			worktreePath := filepath.Join(h.Worktrees(), filepath.Base(h.Root), "task")
			git("worktree", "add", "-b", "fix/source-resume", worktreePath)
			workingDir = worktreePath
			head := git("rev-parse", "HEAD")
			unfinished := filepath.Join(worktreePath, "unfinished.txt")
			if err := os.WriteFile(unfinished, []byte("author repair in progress"), 0o600); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", FirstGen: "generation-1", Backend: "native", Harness: "codex", Model: "fixture-model", Effort: "default", Kind: "ship", Mode: "no-mistakes", HasCredentials: true, Project: h.Root, Worktree: worktreePath, TaskTmp: filepath.Join(h.State, "tasktmp", "task")}
			if err := os.MkdirAll(meta.TaskTmp, 0o700); err != nil {
				t.Fatal(err)
			}
			meta.Brief = filepath.Join(meta.TaskTmp, "brief.md")
			if err := os.WriteFile(meta.Brief, []byte("Retain the original validation history."), 0o600); err != nil {
				t.Fatal(err)
			}
			frozen, err := versionSixPolicy(t, pipeline.Reviewer{}).Select("high-risk")
			if err != nil {
				t.Fatal(err)
			}
			policyPath := filepath.Join(meta.TaskTmp, "pipeline.json")
			if err := frozen.Save(policyPath); err != nil {
				t.Fatal(err)
			}
			meta.PipelineHash, meta.PipelineClass = frozen.Hash, frozen.Class
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteConversation(h.State, state.Conversation{ID: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Session: "fixture-session", ProvedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			database := filepath.Join(nativeRoot, "state.sqlite")
			status := "failed"
			if test.name == "original finishes during memory pause" {
				status = "running"
			}
			schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,last_pushed_sha TEXT,intent TEXT,worktree_dir TEXT,created_at INTEGER);
CREATE TABLE step_results(run_id TEXT,step_name TEXT,round INTEGER,spent INTEGER);
CREATE TABLE findings(run_id TEXT,id TEXT,resolution TEXT);
INSERT INTO repos VALUES('repo-1','%s');
INSERT INTO runs VALUES('original-run','repo-1','fix/source-resume','%s','%s','%s','retain validation history','',1);
INSERT INTO step_results VALUES('original-run','review',3,3);
INSERT INTO findings VALUES('original-run','ci-1','unresolved');`, strings.ReplaceAll(filepath.ToSlash(h.Root), "'", "''"), status, head, head)
			sqlite := func(sql string) {
				t.Helper()
				result, err := commands.Run(t.Context(), execx.Request{Name: "sqlite3", Args: []string{database, sql}})
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("fixture SQLite: %v %s", err, result.Stderr)
				}
			}
			sqlite(schema)
			read := func(path string) []byte {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			policyBefore, databaseBefore := read(policyPath), read(database)
			runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}
			var output, failure bytes.Buffer
			if code := runWithRuntime([]string{"pause", "task", "--operation", "pause-source", "--reason", "memory"}, &output, &failure, commandRuntime{
				resolveHome: runtime.resolveHome,
				taskLifecycle: func(ctx context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
					request.IsWatched = true
					return defaultTaskLifecycle(ctx, h, request, "")
				},
			}); code != 0 {
				t.Fatalf("production pause exit %d: %s", code, &failure)
			}
			paused, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil || paused.GateRun != "original-run" || paused.GateHead != head || paused.GateIntent != "retain validation history" || paused.Session != "fixture-session" || !paused.HandoffSaved || !bytes.Equal(databaseBefore, read(database)) {
				t.Fatalf("production pause lost the original context: %+v, %v", paused, err)
			}
			handoffBefore := read(paused.Handoff)
			if test.name == "original finishes during memory pause" {
				sqlite("UPDATE runs SET status='failed' WHERE id='original-run';")
			} else if test.name == "foreign same-intent run" {
				sqlite("INSERT INTO runs SELECT 'foreign-run',repo_id,branch,status,head_sha,last_pushed_sha,intent,worktree_dir,2 FROM runs WHERE id='original-run';")
			}
			evidenceDatabase := database
			if test.name == "unreadable state" {
				evidenceDatabase += ".retained"
				if err := os.Rename(database, evidenceDatabase); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(database, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			databaseBefore = read(evidenceDatabase)
			userEnvPath := filepath.Join(root, "user-env.json")
			userEnv, err := json.Marshal(environment)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(userEnvPath, userEnv, 0o600); err != nil {
				t.Fatal(err)
			}
			eventsPath := filepath.Join(root, "harness.jsonl")
			instructionPath := filepath.Join(meta.TaskTmp, "instruction.md")
			userEnvironmentCalls, switchCalls := 0, 0
			admit := func() error {
				if test.name == "exhausted allowance" {
					return errors.New("fixture provider allowance exhausted")
				}
				return nil
			}
			launcher := spawn.Service{
				HomeRoot: h.Root, StateDir: h.State, ProjectsRoot: filepath.Join(root, "projects"), ScratchRoot: filepath.Join(root, "scratch"), Commands: commands,
				Worktrees: worktree.Service{Commands: commands, Root: h.Worktrees(), DataDir: h.Data}, Auth: sourceResumeAuth{},
				Harness: harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: sourceResumeAdapter{programPath: program, recordPath: eventsPath, instructionPath: instructionPath, isRefused: test.name == "provider refusal through Switch"}}},
				HostCommand: []string{program, "source-resume-host"},
				UserEnvironment: func() ([]string, error) {
					userEnvironmentCalls++
					data, err := os.ReadFile(userEnvPath)
					if err != nil {
						return nil, err
					}
					var values []string
					err = json.Unmarshal(data, &values)
					return values, err
				},
			}
			t.Cleanup(func() {
				record, err := host.ReadRecord(h.State, meta.ID)
				if errors.Is(err, os.ErrNotExist) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				var handles []windows.Handle
				for _, pid := range []int{record.HostPID, record.ChildPID} {
					handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
					if err != nil {
						t.Errorf("open owned process %d: %v", pid, err)
						continue
					}
					defer windows.CloseHandle(handle)
					handles = append(handles, handle)
				}
				if err := host.Close(h.State, record, 15*time.Second); err != nil {
					t.Error(err)
				}
				for _, handle := range handles {
					if result, err := windows.WaitForSingleObject(handle, 30000); err != nil || result != windows.WAIT_OBJECT_0 {
						t.Errorf("owned process did not exit: %d, %v", result, err)
					}
				}
				if end, err := host.ReadEnd(h.State, meta.ID); err != nil || end.HostPID != record.HostPID || end.ChildPID != record.ChildPID || !end.Closed {
					t.Errorf("owned terminal exit = %+v, %v", end, err)
				}
			})
			runtime.switchTask = func(ctx context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				switchCalls++
				return launcher.Switch(ctx, request)
			}
			service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
				Memory: func() (uint64, uint64, error) {
					if test.name == "low memory" {
						return 4 << 30, 5 << 30, nil
					}
					return 5 << 30, 5 << 30, nil
				},
				Admit: admit,
				Resume: func(ctx context.Context, current state.TaskMeta, prior state.Lifecycle) error {
					return resumeTask(ctx, h, runtime, commands, pipeline.Reader{Root: nativeRoot, Commands: commands}, current, prior, admit)
				},
				Notify: func(state.Lifecycle) error { return nil },
			}}
			runtime.taskLifecycle = func(ctx context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
				return service.Run(ctx, request)
			}

			// Act
			output.Reset()
			failure.Reset()
			code := runWithRuntime([]string{"resume", "task", "--generation", meta.SpawnGen, "--operation", "resume-source"}, &output, &failure, runtime)
			if test.refusal == "" {
				if code != 0 {
					t.Fatalf("composed resume exit %d: %s", code, &failure)
				}
					if code := runWithRuntime([]string{"resume", "task", "--generation", meta.SpawnGen, "--operation", "resume-source"}, &output, &failure, runtime); code != 0 {
					t.Fatalf("resume replay exit %d: %s", code, &failure)
				}
			} else if code != 1 || !strings.Contains(failure.String(), test.refusal) {
				t.Fatalf("refusal exit %d: %s; want %q", code, &failure, test.refusal)
			}

			// Assert
			if calls, err := os.ReadFile(nativeRecord); err != nil && !errors.Is(err, os.ErrNotExist) || strings.Contains(string(calls), "no-mistakes") {
				t.Fatalf("implicit native start/recover/respond occurred: %s, %v", calls, err)
			}
			if !bytes.Equal(databaseBefore, read(evidenceDatabase)) || !bytes.Equal(policyBefore, read(policyPath)) || !bytes.Equal(handoffBefore, read(paused.Handoff)) {
				t.Fatal("resume changed retained native history, frozen policy or handoff bytes")
			}
			if git("rev-parse", "HEAD") != head || string(read(unfinished)) != "author repair in progress" {
				t.Fatal("resume changed authored source")
			}
			current, err := state.ReadTaskMeta(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			retained, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			if test.refusal != "" {
				if !reflect.DeepEqual(current, meta) || !reflect.DeepEqual(retained, paused) {
					t.Fatal("refusal changed task identity or pause context")
				}
				if _, err := os.Stat(eventsPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("refused resume launched a contributor: %v", err)
				}
				if _, err := os.Stat(filepath.Join(h.State, "hosts", meta.ID+".log")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("refused resume started a host: %v", err)
				}
				if test.name == "provider refusal through Switch" && (switchCalls != 1 || userEnvironmentCalls != 1) {
					t.Fatal("provider refusal did not traverse the real Switch preflight")
				}
				return
			}
			if record, err := host.ReadRecord(h.State, meta.ID); err != nil || !host.Running(record) {
				t.Fatalf("the real switch did not leave an owned contributor running: %+v, %v", record, err)
			}
			expected := meta
			expected.SpawnGen, expected.ResumeOperation = current.SpawnGen, "resume-source"
			if current.SpawnGen == meta.SpawnGen || !reflect.DeepEqual(current, expected) || retained.Generation != current.SpawnGen || retained.Session != "fixture-session" || retained.GateRun != "original-run" || retained.GateHead != head || retained.GateIntent != paused.GateIntent || retained.Handoff != paused.Handoff || retained.Phase != "running" || switchCalls != 1 || userEnvironmentCalls != 1 {
				t.Fatalf("resume lost task lineage or validation context: task=%+v lifecycle=%+v switch=%d environment=%d", current, retained, switchCalls, userEnvironmentCalls)
			}
			lines := bytes.Split(bytes.TrimSpace(read(eventsPath)), []byte("\n"))
			if len(lines) != 2 {
				t.Fatalf("contributor launched or received a prompt more than once: %d events", len(lines))
			}
			var launch, delivered sourceResumeEvent
			if err := json.Unmarshal(lines[0], &launch); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(lines[1], &delivered); err != nil {
				t.Fatal(err)
			}
			if launch.Kind != "launch" || !slices.Equal(launch.Args, []string{"resume", "fixture-session", "--source-resume-harness", eventsPath, instructionPath}) || delivered.Kind != "prompt" || strings.Count(delivered.Prompt, "original-run") != 1 || !strings.Contains(delivered.Prompt, " is failed.") || !strings.Contains(delivered.Prompt, paused.Handoff) || !strings.Contains(delivered.Prompt, policyPath) {
				t.Fatalf("actual contributor argv or delivered prompt lost retained context: %+v %+v", launch, delivered)
			}
			t.Log("one contributor relaunched with retained session, handoff, source, run, rounds, spend, findings and frozen policy; OS/auth/admission/provider mocked")
		})
	}
}

func (commands *sourceResumeCommands) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" && request.Args[0] == "-readonly" && commands.queryError != nil {
		return execx.Result{}, commands.queryError
	}
	if request.Name == "no-mistakes" {
		commands.native = append(commands.native, request)
		return execx.Result{ExitCode: 1, Stderr: []byte("validation must be requested explicitly")}, nil
	}
	return (execx.OSRunner{}).Run(ctx, request)
}

func TestPublicResumeRestoresSourceWithoutReplacingValidation(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Fatal("sqlite3 is needed for persisted gate evidence")
	}
	bin := t.TempDir()
	standin.Put(t, filepath.Join(bin, "no-mistakes.exe"))
	standin.Put(t, filepath.Join(bin, "gh.exe"))
	standin.RemoveAtCleanup(t, bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, test := range []struct {
		name, status, reason, until, liveStep, refusal, finalStatus                          string
		isForeign, isUnreadable, isLowMemory, isAllowanceDenied, isProviderRefused, isKilled bool
	}{
		{name: "cancelled", status: "cancelled"},
		{name: "failed", status: "failed"},
		{name: "completed", status: "completed"},
		{name: "interrupted CI monitor", status: "ci_monitor_interrupted"},
		{name: "live original paused for memory", status: "running", reason: "memory"},
		{name: "live original", status: "running"},
		{name: "live original killed while paused", status: "running", isKilled: true},
		{name: "pending original", status: "pending"},
		{name: "live original in Review paused for a dependency", status: "running", reason: "dependency", until: "date:2099-01-01T00:00:00Z", liveStep: "review"},
		{name: "live original in Test paused for a dependency", status: "running", reason: "dependency", until: "date:2099-01-01T00:00:00Z", liveStep: "test"},
		{name: "live original in Test paused for a question", status: "running", reason: "question", until: "question-1", liveStep: "test"},
		{name: "live original finishes while paused", status: "running", finalStatus: "failed"},
		{name: "live original in Test a daemon restart ends while paused", status: "running", reason: "dependency", until: "date:2099-01-01T00:00:00Z", liveStep: "test", finalStatus: "failed"},
		{name: "foreign run with identical intent", status: "running", isForeign: true, refusal: "another validation run owns"},
		{name: "unreadable gate state", status: "cancelled", isUnreadable: true, refusal: "state query failed"},
		{name: "memory admission", status: "failed", isLowMemory: true, refusal: "Resume needs at least 5 GB"},
		{name: "allowance admission", status: "failed", isAllowanceDenied: true, refusal: "provider allowance exhausted"},
		{name: "provider safety refusal", status: "failed", isProviderRefused: true, refusal: "provider refused this session"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			profile := t.TempDir()
			for _, name := range []string{"USERPROFILE", "HOME", "APPDATA", "LOCALAPPDATA"} {
				t.Setenv(name, profile)
			}
			t.Setenv("CFO_HOME", h.Root)
			t.Setenv("CFO_STATE_OVERRIDE", h.State)
			nativeRoot := t.TempDir()
			t.Setenv("NM_HOME", nativeRoot)
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			nativeRecord := filepath.Join(t.TempDir(), "native-commands.txt")
			for _, entry := range standin.Env(nativeRecord,
				standin.Rule{Args: "pr list", Prefix: true, Stdout: "[]"},
				standin.Rule{Any: true, Exit: 1, Stderr: "validation must be requested explicitly"}) {
				name, value, _ := strings.Cut(entry, "=")
				t.Setenv(name, value)
			}
			if effective, err := os.UserHomeDir(); err != nil || effective != profile {
				t.Fatalf("effective home = %q, %v", effective, err)
			}
			commands := &sourceResumeCommands{}
			workingDir := h.Root
			git := func(args ...string) string {
				t.Helper()
				result, err := commands.Run(t.Context(), execx.Request{Dir: workingDir, Name: "git", Args: args})
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("git %v: %v %s", args, err, result.Stderr)
				}
				return strings.TrimSpace(string(result.Stdout))
			}
			git("add", "AGENTS.md")
			git("commit", "-m", "test: retain source")
			worktree := filepath.Join(h.Worktrees(), filepath.Base(h.Root), "task")
			git("worktree", "add", "-b", "fix/source-resume", worktree)
			workingDir = worktree
			head := git("rev-parse", "HEAD")
			// The gate repository holds the run's head, as the daemon's does,
			// so anything that set out to interrupt the run would reach its
			// abort.
			git("clone", "--bare", "--quiet", worktree, filepath.Join(nativeRoot, "repos", "repo-1.git"))
			unfinished := filepath.Join(worktree, "unfinished.txt")
			if err := os.WriteFile(unfinished, []byte("author repair in progress"), 0o600); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Mode: "no-mistakes", Project: h.Root, Worktree: worktree, TaskTmp: filepath.Join(h.State, "tasktmp", "task")}
			if err := os.MkdirAll(meta.TaskTmp, 0o700); err != nil {
				t.Fatal(err)
			}
			frozen, err := versionSixPolicy(t, pipeline.Reviewer{}).Select("high-risk")
			if err != nil {
				t.Fatal(err)
			}
			policyPath := filepath.Join(meta.TaskTmp, "pipeline.json")
			if err := frozen.Save(policyPath); err != nil {
				t.Fatal(err)
			}
			meta.PipelineHash, meta.PipelineClass = frozen.Hash, frozen.Class
			policyBefore, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			database := filepath.Join(nativeRoot, "state.sqlite")
			// Review spent its three cycles; a run in Review is still on
			// them, and one in Test has spent its one test repair too.
			steps := "INSERT INTO step_results VALUES('original-run','review','completed',3,3);"
			switch test.liveStep {
			case "review":
				steps = "INSERT INTO step_results VALUES('original-run','review','running',3,3);"
			case "test":
				steps += "\nINSERT INTO step_results VALUES('original-run','test','running',1,1);"
			}
			schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,last_pushed_sha TEXT,intent TEXT,worktree_dir TEXT,created_at INTEGER);
CREATE TABLE step_results(run_id TEXT,step_name TEXT,status TEXT,round INTEGER,spent INTEGER);
CREATE TABLE findings(run_id TEXT,id TEXT,resolution TEXT);
INSERT INTO repos VALUES('repo-1','%s');
INSERT INTO runs VALUES('original-run','repo-1','fix/source-resume','%s','%s','%s','retain validation history','',1);
%s
INSERT INTO findings VALUES('original-run','ci-1','unresolved');`, strings.ReplaceAll(filepath.ToSlash(h.Root), "'", "''"), test.status, head, head, steps)
			result, err := commands.Run(t.Context(), execx.Request{Name: "sqlite3", Args: []string{database, schema}})
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("create gate fixture: %v %s", err, result.Stderr)
			}
			before, err := os.ReadFile(database)
			if err != nil {
				t.Fatal(err)
			}
			launches := 0
			var resumed spawn.SwitchRequest
			runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }, cleanup: defaultCleanup, switchTask: func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				if test.isProviderRefused {
					return spawn.SwitchResult{}, errors.New("provider refused this session")
				}
				launches++
				resumed = request
				return spawn.SwitchResult{}, nil
			}}
			service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
				Memory: func() (uint64, uint64, error) {
					if test.isLowMemory {
						return 4 << 30, 5 << 30, nil
					}
					return 5 << 30, 5 << 30, nil
				},
				Admit: func() error {
					if test.isAllowanceDenied {
						return errors.New("provider allowance exhausted")
					}
					return nil
				},
				Resume: func(ctx context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
					return resumeTask(ctx, h, runtime, commands, pipeline.Reader{Root: nativeRoot, Commands: commands}, meta, prior, nil)
				},
				Notify: func(state.Lifecycle) error { return nil },
			}}
			runtime.taskLifecycle = func(ctx context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
				if request.Action == "pause" || request.Action == "stop" {
					request.IsWatched = true
					return defaultTaskLifecycle(ctx, h, request, "")
				}
				return service.Run(ctx, request)
			}
			var output, failure bytes.Buffer
			reason := "overlord"
			if test.reason != "" {
				reason = test.reason
			}
			pause := []string{"pause", "task", "--operation", "pause-source", "--reason", reason}
			if test.until != "" {
				pause = append(pause, "--until", test.until)
			}
			if code := runWithRuntime(pause, &output, &failure, runtime); code != 0 {
				t.Fatalf("public pause exit %d: %s", code, &failure)
			}
			paused, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil || !paused.HandoffSaved || paused.GateRun != "original-run" || paused.GateIntent != "retain validation history" || paused.GateHead != head {
				t.Fatalf("pause = %+v, %v", paused, err)
			}
			if after, err := os.ReadFile(database); err != nil || !bytes.Equal(before, after) {
				t.Fatalf("production pause changed validation history: %v", err)
			}
			// Whatever the reason, a pause leaves the run to go on: it neither
			// aborts it nor fails trying (issue 2089).
			if calls, err := os.ReadFile(nativeRecord); err != nil && !errors.Is(err, os.ErrNotExist) || strings.Contains(string(calls), "no-mistakes") || slices.ContainsFunc(paused.Problems, func(problem string) bool { return strings.Contains(problem, "gate") }) || slices.ContainsFunc(paused.Stopped, func(stopped string) bool { return strings.Contains(stopped, "original-run") }) {
				t.Fatalf("a %s pause reached into the validation run: native calls %q, problems %q, stopped %q, %v", reason, calls, paused.Problems, paused.Stopped, err)
			}
			if test.isKilled {
				// A cleanup retires no task around the run its pause left
				// running: it is refused with nothing changed, naming the kill
				// that ends the run.
				if code := runWithRuntime([]string{"cleanup", meta.ID}, &output, &failure, runtime); code != 1 || !strings.Contains(failure.String(), "cfo kill "+meta.ID) {
					t.Fatalf("cleanup of a goblin paused with an open run exit %d: %s", code, &failure)
				}
				current, err := state.ReadLifecycle(h.State, meta.ID)
				if _, metaErr := state.ReadTaskMeta(h.State, meta.ID); metaErr != nil || err != nil || !reflect.DeepEqual(current, paused) {
					t.Fatalf("refused cleanup changed the task: %+v, %v, %v", current, err, metaErr)
				}
				if content, err := os.ReadFile(unfinished); err != nil || string(content) != "author repair in progress" {
					t.Fatalf("refused cleanup touched the unfinished repair: %q, %v", content, err)
				}
				// A stop is what aborts the run, and the stop reports the
				// abort's refusal.
				failure.Reset()
				if code := runWithRuntime([]string{"kill", meta.ID, "--operation", "kill-source"}, &output, &failure, runtime); code != 1 || !strings.Contains(failure.String(), "validation must be requested explicitly") {
					t.Fatalf("kill of a goblin paused with an open run exit %d: %s", code, &failure)
				}
				if calls, err := os.ReadFile(nativeRecord); err != nil || !strings.Contains(string(calls), "no-mistakes axi abort --run original-run") {
					t.Fatalf("the kill did not abort the validation run: native calls %q, %v", calls, err)
				}
				return
			}
			if test.isForeign || test.finalStatus != "" {
				update := "INSERT INTO runs SELECT 'foreign-run',repo_id,branch,status,head_sha,last_pushed_sha,intent,worktree_dir,2 FROM runs WHERE id='original-run';"
				if test.finalStatus != "" {
					// The run ends on its own, as a daemon restart ends a run
					// whose step was executing, and its spending stays as it was.
					update = fmt.Sprintf("UPDATE runs SET status='%[1]s' WHERE id='original-run'; UPDATE step_results SET status='%[1]s' WHERE status='running';", test.finalStatus)
				}
				result, err := commands.Run(t.Context(), execx.Request{Name: "sqlite3", Args: []string{database, update}})
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("advance isolated native fixture: %v %s", err, result.Stderr)
				}
				before, err = os.ReadFile(database)
				if err != nil {
					t.Fatal(err)
				}
			}
			if test.isUnreadable {
				commands.queryError = errors.New("fixture database locked")
			}

			// Act
			for attempt := 0; attempt < 2; attempt++ {
				output.Reset()
				failure.Reset()
				code := runWithRuntime([]string{"resume", "task", "--operation", "resume-source"}, &output, &failure, runtime)
				if test.refusal != "" {
					if code != 1 || !strings.Contains(failure.String(), test.refusal) {
						t.Fatalf("resume exit %d: %s, want %q", code, &failure, test.refusal)
					}
					break
				}
				if code != 0 {
					t.Fatalf("public resume exit %d: %s; native calls = %d", code, &failure, len(commands.native))
				}
			}

			// Assert
			if calls, err := os.ReadFile(nativeRecord); err != nil && !errors.Is(err, os.ErrNotExist) || strings.Contains(string(calls), "no-mistakes") {
				t.Fatalf("production pause invoked native mutation: %s, %v", calls, err)
			}
			after, err := os.ReadFile(database)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("original run, rounds, spent repairs or findings changed: %v", err)
			}
			policyAfter, err := os.ReadFile(policyPath)
			if err != nil || !bytes.Equal(policyBefore, policyAfter) {
				t.Fatalf("frozen high-risk policy changed: %v", err)
			}
			if git("rev-parse", "HEAD") != head {
				t.Fatal("resume moved the author's source")
			}
			if content, err := os.ReadFile(unfinished); err != nil || string(content) != "author repair in progress" {
				t.Fatalf("unfinished repair = %q, %v", content, err)
			}
			if test.refusal != "" {
				current, err := state.ReadLifecycle(h.State, meta.ID)
				if err != nil || !reflect.DeepEqual(current, paused) || launches != 0 || len(commands.native) != 0 {
					t.Fatalf("refusal changed pause or validation: %+v, %v, launches = %d, native calls = %d", current, err, launches, len(commands.native))
				}
				return
			}
			if len(commands.native) != 0 || launches != 1 || resumed.ResumeHandoff != paused.Handoff || !resumed.ForceDirty {
				t.Fatalf("resume = %+v, launches = %d, native calls = %d", resumed, launches, len(commands.native))
			}
			if strings.Count(resumed.ResumeNote, "original-run") != 1 {
				t.Fatalf("original validation context must be named once: %q", resumed.ResumeNote)
			}
			if test.finalStatus != "" && !strings.Contains(resumed.ResumeNote, " is "+test.finalStatus+".") {
				t.Fatalf("resume lost the original run's terminal status: %q", resumed.ResumeNote)
			}
			current, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil || current.GateRun != paused.GateRun || current.GateHead != head || current.Phase != "running" {
				t.Fatalf("retained lifecycle = %+v, %v", current, err)
			}
		})
	}
}
