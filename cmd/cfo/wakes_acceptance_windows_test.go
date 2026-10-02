package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// TestWakesReachEveryCFOHarness is the end-to-end proof that a wake reaches a
// real CFO of each harness: a Claude Code CFO through its Stop hook, and a
// Codex and a pi CFO through the line cfo serve types into their native
// terminal. Each runs in a scratch home under `cfo serve --example`, with a
// real goblin of another harness idle at its prompt, which then reports to the
// CFO. Each wake must be raised exactly once and acknowledged by that CFO
// running cfo drain.
//
// It runs real harnesses on the Overlord's subscriptions and a few pi turns
// on its configured provider, so it runs only when asked:
//
//	CFO_WAKES_REAL=1 CFO_WAKES_BINARY=<cfo.exe built from this tree>
//	CFO_WAKES_PROJECT=<a path Claude Code already trusts that nothing occupies>
//	CFO_WAKES_RESULTS=<a directory for the record> [CFO_WAKES_ONLY=claude|codex|pi]
func TestWakesReachEveryCFOHarness(t *testing.T) {
	if os.Getenv("CFO_WAKES_REAL") != "1" {
		t.Skip("set CFO_WAKES_REAL=1 with CFO_WAKES_BINARY, CFO_WAKES_PROJECT and CFO_WAKES_RESULTS to prove the wakes with real harnesses")
	}
	binary, project, results := os.Getenv("CFO_WAKES_BINARY"), os.Getenv("CFO_WAKES_PROJECT"), os.Getenv("CFO_WAKES_RESULTS")
	if binary == "" || project == "" || results == "" {
		t.Fatal("CFO_WAKES_BINARY, CFO_WAKES_PROJECT and CFO_WAKES_RESULTS are all required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CFO_HOME and CFO_STATE_OVERRIDE first", root, state)
	}
	for i, pair := range [][2]string{{"claude", "codex"}, {"codex", "pi"}, {"pi", "claude"}} {
		if only := os.Getenv("CFO_WAKES_ONLY"); only != "" && only != pair[0] {
			continue
		}
		t.Run(pair[0]+"-cfo", func(t *testing.T) {
			proveWakes(t, &wakeProof{binary: binary, project: project, root: filepath.Join(results, pair[0]+"-cfo"), cfo: pair[0], goblin: pair[1], port: 4392 + i})
		})
	}
}

// proofModel is the model a harness runs on in the proof, or "" for its own
// default. Codex is named one: on 2026-10-01 its configured default,
// gpt-6.1-sol, was refused for the signed-in ChatGPT account, and the CFO's
// first turn never ran.
func proofModel(harness string) string {
	if harness == "codex" {
		return "gpt-6-astra"
	}
	return ""
}

type wakeProof struct {
	binary, project, root, cfo, goblin string
	port                               int
	home                               home.Home
	env                                []string
	log                                *os.File
	// cfoTerminal is the CFO's native terminal once it has started, whose
	// screen a wait that gives up records.
	cfoTerminal *host.Record
	// seen is every record the queue has held since the proof began, by
	// sequence, so a record the CFO acked between two looks still counts.
	mu   sync.Mutex
	seen map[int]wake.Record
}

func (p *wakeProof) say(format string, args ...any) {
	line := time.Now().UTC().Format("15:04:05Z ") + fmt.Sprintf(format, args...)
	fmt.Fprintln(p.log, line)
}

func proveWakes(t *testing.T, p *wakeProof) {
	if err := os.MkdirAll(p.root, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(p.root, "proof.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	p.log = logFile
	p.home = home.Home{Root: filepath.Join(p.root, "home"), State: filepath.Join(p.root, "home", "state"), Data: filepath.Join(p.root, "home", "data")}
	for _, dir := range []string{p.home.State, p.home.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch CFO home for the wake proof\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	p.say("scratch home %s; CFO %s, goblin %s", p.home.Root, p.cfo, p.goblin)
	p.seen = map[int]wake.Record{}
	watching, stopWatching := context.WithCancel(context.Background())
	watched := make(chan struct{})
	go func() { defer close(watched); p.watchQueue(watching) }()
	defer func() { stopWatching(); <-watched }()
	p.setUpProject(t)
	p.env = p.environment()

	serve := exec.Command(p.binary, "serve", "--example", "--listen", fmt.Sprintf("127.0.0.1:%d", p.port))
	serve.Env = p.env
	serve.Stdout, serve.Stderr = logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _, _ = serve.Process.Wait() })
	p.say("cfo serve --example pid %d", serve.Process.Pid)
	p.await(t, "the board record", time.Minute, func() bool { return exists(filepath.Join(p.home.State, "board.json")) })

	// The goblin is spawned before the CFO starts, as a CFO's own goblins are
	// spawned in its turn: a Claude Code CFO's Stop hook arms only while a
	// task is in flight, so one whose first turn ended with none would never
	// stop again to arm it, and no wake could reach it.
	goblin := "idle-" + p.goblin
	// A proof that fails still ends its goblin, whose terminal's host would
	// otherwise outlive it; the log is still open while deferred calls run.
	// cfo cleanup refuses a goblin in a turn, as one is when its CFO messaged
	// it (a pi CFO did, live, on 2026-10-01), so the host this proof started
	// is then stopped by its pid.
	defer func() {
		command := exec.Command(p.binary, "cleanup", goblin)
		command.Env = p.env
		output, err := command.CombinedOutput()
		p.say("cfo cleanup %s (error %v):\n%s", goblin, err, output)
		record, readErr := host.ReadRecord(p.home.State, goblin)
		if err == nil || readErr != nil {
			return
		}
		// Only this proof's host for the goblin, never a process that took
		// its pid after it ended.
		arguments, argsErr := proc.Arguments(record.HostPID)
		if argsErr != nil || len(arguments) == 0 || !strings.EqualFold(arguments[0], p.binary) || !slices.Contains(arguments, "host") || !slices.Contains(arguments, goblin) {
			p.say("leaving pid %d alone: it is not this proof's host for %s (%v)", record.HostPID, goblin, argsErr)
			return
		}
		if process, err := os.FindProcess(record.HostPID); err == nil {
			p.say("stopping %s's host pid %d: %v", goblin, record.HostPID, process.Kill())
		}
	}()
	p.cfoCommand(t, "spawn", goblin, "--project", p.project, "--brief", p.brief(t, goblin, "This is a supervision fixture. Run the command git status once, then reply with the single word ready and end your turn. Change no files and run no cfo command."), "--mode", "local-only", "--harness", p.goblin, "--model", proofModel(p.goblin))

	cfo := p.startCFO(t)
	p.cfoTerminal = &cfo
	p.say("CFO %s runs in native terminal cfo, host pid %d, harness pid %d", p.cfo, cfo.HostPID, cfo.ChildPID)
	if p.cfo == "claude" {
		// The hook holds its lock only until it rewakes the CFO, and records
		// every arming in its epoch ledger.
		p.await(t, "the Claude Code CFO's Stop hook to arm", 5*time.Minute, func() bool {
			return exists(filepath.Join(p.home.State, ".claude-autoarm.lock")) || exists(filepath.Join(p.home.State, ".claude-autoarm-epoch"))
		})
	} else {
		p.await(t, "the CFO to register", 5*time.Minute, func() bool {
			primary, live := livePrimaryFile(p.home.State)
			return live && primary == p.cfo
		})
	}
	p.say("CFO ready")
	idle := p.expectOneWake(t, "the idle goblin", 10*time.Minute, func(r wake.Record) bool {
		return r.Kind == "stale" && r.Key == goblin && (strings.HasPrefix(r.Detail, "goblin_idle:") || strings.HasPrefix(r.Detail, "awaiting_answer:"))
	})
	p.expectAcked(t, idle)

	// A working report is status for the board and wakes nobody; a failure
	// wakes the CFO.
	p.cfoCommand(t, "notify", goblin, "--failed", "the fixture goblin reports to its CFO")
	report := p.expectOneWake(t, "the goblin's report", 5*time.Minute, func(r wake.Record) bool {
		return r.Kind == "notify" && r.Key == goblin && strings.Contains(r.Detail, "reports to its CFO")
	})
	p.expectAcked(t, report)

	if screen, err := host.ReadScreen(cfo); err == nil {
		p.say("the CFO's screen ends:\n%s", host.ScreenTail(screen, 20))
	}
	if records, err := wake.Pending(p.home.State); err == nil {
		p.say("records still queued: %d", len(records))
	}
}

// setUpProject makes the scratch project, a checkout with an origin, at the
// path Claude Code already trusts, and removes it when the proof ends.
func (p *wakeProof) setUpProject(t *testing.T) {
	if exists(p.project) {
		t.Fatalf("%s already exists; the proof makes its project fresh", p.project)
	}
	origin := filepath.Join(p.root, "origin.git")
	for _, args := range [][]string{
		{"init", "-q", "--bare", "--initial-branch=main", origin},
		{"init", "-q", "--initial-branch=main", p.project},
		{"-C", p.project, "config", "user.name", "Wake proof fixture"},
		{"-C", p.project, "config", "user.email", "fixture@example.invalid"},
		{"-C", p.project, "config", "core.autocrlf", "false"},
	} {
		runGit(t, args...)
	}
	writeProofFile(t, filepath.Join(p.project, ".gitignore"), ".worktrees/\n")
	writeProofFile(t, filepath.Join(p.project, "README.md"), "# Wake proof fixture\n")
	for _, args := range [][]string{
		{"-C", p.project, "add", "."},
		{"-C", p.project, "commit", "-qm", "Initialize the wake proof fixture"},
		{"-C", p.project, "remote", "add", "origin", origin},
		{"-C", p.project, "push", "-q", "origin", "main"},
		{"-C", p.project, "remote", "set-head", "origin", "main"},
	} {
		runGit(t, args...)
	}
	t.Cleanup(func() { _ = os.RemoveAll(p.project) })
}

// environment is what cfo serve, spawn and the CFO run with: this user's
// environment without the session that runs this test, pointed at the
// scratch home, with this tree's cfo first on PATH.
func (p *wakeProof) environment() []string {
	userEnv, err := spawn.UserEnvironment()
	if err != nil {
		userEnv = os.Environ()
	}
	env := nativeCFOEnvironment(userEnv, nil, p.home, p.root)
	for i, entry := range env {
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "PATH") {
			env[i] = name + "=" + filepath.Dir(p.binary) + ";" + value
		}
	}
	return env
}

// startCFO starts the CFO in native terminal cfo with its instructions as its
// first prompt, answers the startup dialogs its harness shows, and returns
// its host's record. A Claude Code CFO loads this tree's hooks from its own
// settings and none of the user's, which name the fleet's binary.
func (p *wakeProof) startCFO(t *testing.T) host.Record {
	prompt := "You stand in for the CFO of a scratch Code Goblins home in an automated test. Use the shell only as told. First run the command cfo register then reply standing by and end your turn. After that, every time a line starting with cfo watcher wake reaches you, run cfo drain, then run the WAKE_ACK_REQUIRED command it prints exactly as printed, adding --ack-blocking only if it is refused, then reply with one short line naming each drained record by kind and key, and end your turn. Run no other command and change no file."
	var args []string
	var err error
	switch p.cfo {
	case "claude":
		settings := filepath.Join(p.root, "claude-settings.json")
		hook := func(name string, extra map[string]any) map[string]any {
			entry := map[string]any{"type": "command", "command": p.binary, "args": []string{"hook", name}}
			for key, value := range extra {
				entry[key] = value
			}
			return map[string]any{"hooks": []any{entry}}
		}
		data, _ := json.Marshal(map[string]any{"hooks": map[string]any{
			"SessionStart": []any{hook("session-start", map[string]any{"timeout": 120})},
			"Stop":         []any{hook("turnend-guard", nil), hook("stop-autoarm", map[string]any{"asyncRewake": true, "timeout": 28800})},
		}})
		writeProofFile(t, settings, string(data))
		args, err = nativeCFOProgram("claude")
		args = append(args, "--setting-sources", "project,local", "--settings", settings, "--dangerously-skip-permissions", prompt)
	case "codex":
		args, err = spawn.NativeProgram("codex", "--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "-m", proofModel("codex"), prompt)
	case "pi":
		args, err = spawn.NativeProgram("pi", "--approve", prompt)
	}
	if err != nil {
		t.Fatal(err)
	}
	record, err := host.Launch(p.home.State, []string{p.binary, "host"}, p.env, host.Spec{ID: "cfo", Args: args, Dir: p.project, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if process, err := os.FindProcess(record.HostPID); err == nil {
			_ = process.Kill()
		}
	})
	return p.settle(t, record)
}

// settle answers the startup dialogs the CFO's harness shows in record's
// terminal, and returns once its composer or a turn has settled.
func (p *wakeProof) settle(t *testing.T, record host.Record) host.Record {
	screens, _ := harness.NativeScreens(harness.Kind(p.cfo))
	// A dialog is answered before anything else, and the composer or a turn
	// counts only once it has shown startupSettle reads in a row: on
	// 2026-10-01 a Codex CFO showed one of them for a moment before its
	// directory trust prompt drew, and the proof waited behind that prompt.
	const startupSettle = 5
	settled := 0
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		screen, err := host.ReadScreen(record)
		if dialog, shows := screens.Dialog(screen); err == nil && shows && dialog.Accept != "" {
			settled = 0
			key := "\x1b[B"
			if focused, ok := dialog.Focused(screen); ok && dialog.Chosen(focused) {
				key = "\r"
			}
			p.say("answering %s with %q", dialog.Name, key)
			if client, err := host.Dial(record); err == nil {
				_ = client.Input([]byte(key))
				_ = client.Close()
			}
		} else if err == nil && (screens.IsWorking(screen) || screens.IsReady(screen)) {
			if settled++; settled >= startupSettle {
				return record
			}
		} else {
			settled = 0
		}
		time.Sleep(time.Second)
	}
	screen, _ := host.ReadScreen(record)
	t.Fatalf("the %s CFO never showed its composer; its screen ends:\n%s", p.cfo, host.ScreenTail(screen, 12))
	return record
}

// livePrimaryFile reads the harness the registered CFO runs, and whether it
// is registered and alive.
func livePrimaryFile(stateDir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(stateDir, "primary.json"))
	if err != nil {
		return "", false
	}
	var primary struct {
		Agent string `json:"agent"`
		Host  string `json:"host"`
	}
	if json.Unmarshal(data, &primary) != nil || primary.Host != "cfo" {
		return "", false
	}
	return primary.Agent, true
}

func (p *wakeProof) brief(t *testing.T, id, task string) string {
	path := filepath.Join(p.home.Data, id, "brief.md")
	writeProofFile(t, path, "# Brief "+id+"\n\n## Project\n\n"+p.project+"\n\n## Task\n\n"+task+"\n")
	return path
}

// cfoCommand runs this tree's cfo in the scratch home.
func (p *wakeProof) cfoCommand(t *testing.T, args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, p.binary, args...)
	command.Env = p.env
	output, err := command.CombinedOutput()
	p.say("cfo %s:\n%s", strings.Join(args, " "), output)
	if err != nil {
		t.Fatalf("cfo %s: %v", strings.Join(args, " "), err)
	}
}

// expectOneWake waits for the first queued record matching want, and then
// checks no second one matching it appears while the CFO handles it.
func (p *wakeProof) expectOneWake(t *testing.T, what string, within time.Duration, want func(wake.Record) bool) wake.Record {
	p.say("waiting for the wake for %s", what)
	var found []wake.Record
	seen := map[int]bool{}
	collect := func() {
		for _, record := range p.records() {
			if want(record) && !seen[record.Seq] {
				seen[record.Seq] = true
				found = append(found, record)
			}
		}
	}
	p.await(t, "the wake for "+what, within, func() bool { collect(); return len(found) > 0 })
	p.say("wake %d %s %s: %s", found[0].Seq, found[0].Kind, found[0].Key, found[0].Detail)
	return found[0]
}

// watchQueueEvery is how often the proof reads the queue. A CFO takes a turn
// of several seconds to drain and ack, so no record comes and goes between
// two reads.
const watchQueueEvery = 500 * time.Millisecond

// watchQueue keeps every record the queue holds in p.seen until ctx ends.
func (p *wakeProof) watchQueue(ctx context.Context) {
	ticker := time.NewTicker(watchQueueEvery)
	defer ticker.Stop()
	for {
		if records, err := wake.Pending(p.home.State); err == nil {
			p.mu.Lock()
			for _, record := range records {
				p.seen[record.Seq] = record
			}
			p.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// records is every record the queue has held since the proof began, acked
// or not, in sequence order.
func (p *wakeProof) records() []wake.Record {
	p.mu.Lock()
	defer p.mu.Unlock()
	records := make([]wake.Record, 0, len(p.seen))
	for _, record := range p.seen {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Seq < records[j].Seq })
	return records
}

// expectAcked waits for the CFO to drain and acknowledge record, which is the
// proof the wake reached it, and then that no second wake of the same kind
// and key followed.
func (p *wakeProof) expectAcked(t *testing.T, record wake.Record) {
	p.await(t, fmt.Sprintf("the CFO to ack wake %d", record.Seq), 8*time.Minute, func() bool {
		acked, err := wake.Acked(p.home.State, record.Seq)
		return err == nil && acked
	})
	p.say("the CFO acked wake %d", record.Seq)
	if data, err := os.ReadFile(filepath.Join(p.home.State, ".cfo-wake-typed")); err == nil {
		p.say("typed wake lines have covered through: %s", strings.TrimSpace(string(data)))
	}
	for _, later := range p.records() {
		if later.Seq > record.Seq && later.Kind == record.Kind && later.Key == record.Key {
			t.Errorf("a second %s wake keyed %s followed wake %d: %d %s", record.Kind, record.Key, record.Seq, later.Seq, later.Detail)
		}
	}
}

func (p *wakeProof) await(t *testing.T, what string, within time.Duration, done func() bool) {
	deadline := time.Now().Add(within)
	for !done() {
		if time.Now().After(deadline) {
			p.say("gave up waiting for %s after %s", what, within)
			if p.cfoTerminal != nil {
				screen, err := host.ReadScreen(*p.cfoTerminal)
				p.say("the CFO's screen then (read error %v):\n%s", err, host.ScreenTail(screen, 40))
			}
			t.Fatalf("gave up waiting for %s after %s; see %s", what, within, filepath.Join(p.root, "proof.log"))
		}
		time.Sleep(2 * time.Second)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeProofFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, args ...string) {
	t.Helper()
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
