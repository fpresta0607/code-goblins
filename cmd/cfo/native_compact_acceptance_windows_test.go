package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// TestANativeCFOCompactionCheckpointsThenWakesOnce is the end-to-end proof
// that a real Codex or pi CFO gets what a Claude Code CFO gets around a
// compaction: the checkpoint written before the compaction ends, then exactly
// one check wake keyed compact naming it, which cfo serve types into the idle
// CFO's native terminal and the CFO drains and acknowledges. A wake queued
// after that still reaches it. Each CFO runs in a scratch home under `cfo
// serve --example`, with a configuration folder of its own that the proof
// installs the native hooks into, and compacts once.
//
// It runs real harnesses on the Overlord's subscriptions, so it runs only
// when asked:
//
//	CFO_NATIVE_COMPACT_REAL=1 CFO_NATIVE_COMPACT_BINARY=<cfo.exe built from this tree>
//	CFO_NATIVE_COMPACT_RESULTS=<an unoccupied directory under the temporary directory>
//	CFO_NATIVE_COMPACT_PI_DIR=<a pi configuration folder signed in to a subscription>
//	[CFO_NATIVE_COMPACT_ONLY=codex|pi]
func TestANativeCFOCompactionCheckpointsThenWakesOnce(t *testing.T) {
	if os.Getenv("CFO_NATIVE_COMPACT_REAL") != "1" {
		t.Skip("set CFO_NATIVE_COMPACT_REAL=1 with CFO_NATIVE_COMPACT_BINARY, CFO_NATIVE_COMPACT_RESULTS and CFO_NATIVE_COMPACT_PI_DIR to prove the compaction with real Codex and pi CFOs")
	}
	binary, results := os.Getenv("CFO_NATIVE_COMPACT_BINARY"), os.Getenv("CFO_NATIVE_COMPACT_RESULTS")
	if !filepath.IsAbs(binary) || !filepath.IsAbs(results) || exists(results) {
		t.Fatal("the proof needs an absolute CFO_NATIVE_COMPACT_BINARY and an unoccupied absolute CFO_NATIVE_COMPACT_RESULTS")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CFO_HOME and CFO_STATE_OVERRIDE first", root, state)
	}
	for i, cfo := range []string{"codex", "pi"} {
		if only := os.Getenv("CFO_NATIVE_COMPACT_ONLY"); only != "" && only != cfo {
			continue
		}
		t.Run(cfo+"-cfo", func(t *testing.T) {
			root := filepath.Join(results, cfo+"-cfo")
			proveNativeCompact(t, &wakeProof{binary: binary, project: filepath.Join(root, "project"), root: root, cfo: cfo, port: 4396 + i})
		})
	}
}

func proveNativeCompact(t *testing.T, p *wakeProof) {
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
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch CFO home for the native compaction proof\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	p.say("scratch home %s; CFO %s", p.home.Root, p.cfo)
	p.seen = map[int]wake.Record{}
	watching, stopWatching := context.WithCancel(context.Background())
	watched := make(chan struct{})
	go func() { defer close(watched); p.watchQueue(watching) }()
	defer func() { stopWatching(); <-watched }()
	p.setUpProject(t)
	p.setUpForge(t)
	configDir, configVariable := p.nativeConfig(t)
	// The CFO runs on a subscription only: no variable a harness or provider
	// reads as a metered key reaches it, so a fallback finds no key rather
	// than spending one.
	p.env = slices.DeleteFunc(p.environment(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(name, "CODEX_HOME") || strings.EqualFold(name, "PI_CODING_AGENT_DIR") || strings.HasSuffix(strings.ToUpper(name), "_API_KEY")
	})
	p.env = append(p.env, configVariable+"="+configDir, "PI_SKIP_VERSION_CHECK=1")
	if !strings.HasPrefix(strings.ToLower(p.home.Root), strings.ToLower(userTemp(p.env))+string(filepath.Separator)) {
		t.Fatalf("the scratch home %s must be under the temporary directory %s, which cfo serve --example requires", p.home.Root, userTemp(p.env))
	}
	p.cfoCommand(t, "hooks", "install", p.cfo, "--config-dir", configDir)
	p.expectCompactHooks(t, configDir)

	serve := exec.Command(p.binary, "serve", "--example", "--listen", fmt.Sprintf("127.0.0.1:%d", p.port))
	serve.Env = p.env
	serve.Stdout, serve.Stderr = logFile, logFile
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serve.Process.Kill(); _, _ = serve.Process.Wait() })
	p.say("cfo serve --example pid %d", serve.Process.Pid)
	p.await(t, "the board record", time.Minute, func() bool { return exists(filepath.Join(p.home.State, "board.json")) })

	cfo := p.startCompactCFO(t)
	p.cfoTerminal = &cfo
	p.say("CFO %s runs in native terminal cfo, host pid %d, harness pid %d", p.cfo, cfo.HostPID, cfo.ChildPID)
	noticing, stopNoticing := context.WithCancel(context.Background())
	noticed := make(chan struct{})
	go func() { defer close(noticed); p.dismissOptionalNotices(noticing, cfo) }()
	defer func() { stopNoticing(); <-noticed }()
	p.await(t, "the CFO to register and end its first turn", 5*time.Minute, func() bool {
		primary, live := livePrimaryFile(p.home.State)
		return live && primary == p.cfo && p.idle(cfo)
	})
	p.say("CFO registered and idle")

	asked := time.Now()
	p.typeLine(t, cfo, "In one short line, say what you will do when a cfo watcher wake line reaches you. Run no command.")
	p.await(t, "the CFO's second turn", 5*time.Minute, func() bool {
		received, err := supervisor.NativeHostPromptSince(p.home.State, "cfo", asked)
		return err == nil && received && p.idle(cfo)
	})
	checkpoint := filepath.Join(p.home.State, digest.CheckpointFile)
	if exists(checkpoint) {
		t.Fatalf("a checkpoint exists before the compaction: %s", checkpoint)
	}
	p.say("conversation held; no checkpoint yet")

	compactAt := time.Now()
	p.typeLine(t, cfo, "/compact")
	p.say("typed /compact")
	// A completion list can take the first Enter; the second submits the
	// command it completed.
	time.Sleep(10 * time.Second)
	if !exists(checkpoint) && p.shows(cfo, "/compact", 1) {
		if client, err := host.Dial(cfo); err == nil {
			_ = client.Input([]byte("\r"))
			_ = client.Close()
			p.say("pressed Enter again: /compact still showed with no checkpoint")
		}
	}
	var written time.Time
	p.await(t, "the checkpoint written before the compaction", 5*time.Minute, func() bool {
		info, err := os.Stat(checkpoint)
		if err != nil || info.ModTime().Before(compactAt) {
			return false
		}
		written = info.ModTime()
		return true
	})
	p.say("checkpoint %s written %s", checkpoint, written.UTC().Format(time.RFC3339Nano))
	compacted := p.expectOneWake(t, "the compaction", 10*time.Minute, func(r wake.Record) bool {
		return r.Kind == "check" && r.Key == "compact"
	})
	if !strings.Contains(compacted.Detail, checkpoint) || !strings.Contains(compacted.Detail, written.UTC().Format(time.RFC3339)) {
		t.Errorf("the compact wake does not name the checkpoint %s written %s: %s", checkpoint, written.UTC().Format(time.RFC3339), compacted.Detail)
	}
	if final, err := os.Stat(checkpoint); err != nil || !final.ModTime().Equal(written) {
		t.Errorf("the checkpoint changed after the compaction began (%v): %v", err, final)
	}
	if !written.Before(compacted.Time) {
		t.Errorf("the checkpoint was written %s, not before the post-compact hook queued its wake %s", written, compacted.Time)
	}
	ended, err := p.compactionEnded(configDir, compactAt)
	if err != nil {
		t.Errorf("the %s record of the compaction: %v", p.cfo, err)
	} else {
		p.say("the %s record of the compaction is dated %s", p.cfo, ended.UTC().Format(time.RFC3339Nano))
		if !written.Before(ended) {
			t.Errorf("the checkpoint was written %s, not before the compaction ended %s", written, ended)
		}
	}

	p.await(t, "the wake line typed into the idle CFO and taken as its prompt", 5*time.Minute, func() bool {
		received, err := supervisor.NativeHostPromptSince(p.home.State, "cfo", compacted.Time)
		return err == nil && received
	})
	p.say("the CFO's native hooks report a prompt after the wake was queued; its screen shows the wake line: %v", p.shows(cfo, "cfo watcher wake", 1))
	p.expectAcked(t, compacted)

	after, err := wake.Append(p.home.State, "heartbeat", "after-compact", "the native compaction proof checks the CFO is still supervised")
	if err != nil {
		t.Fatal(err)
	}
	p.say("queued wake %d heartbeat after-compact", after.Seq)
	p.expectAcked(t, after)
	if primary, live := livePrimaryFile(p.home.State); !live || primary != p.cfo {
		t.Errorf("the CFO is no longer registered after its compaction: %q, live %v", primary, live)
	}

	compactWakes := 0
	for _, record := range p.records() {
		if record.Kind == "check" && record.Key == "compact" {
			compactWakes++
		}
	}
	p.say("compact wakes through the whole proof: %d", compactWakes)
	if compactWakes != 1 {
		t.Errorf("%d compact wakes, want exactly one", compactWakes)
	}
	if screen, err := host.ReadScreen(cfo); err == nil {
		p.say("the CFO's screen ends:\n%s", host.ScreenTail(screen, 30))
	}
	if records, err := wake.Pending(p.home.State); err == nil {
		p.say("records still queued: %d", len(records))
	}
}

// nativeConfig is the CFO harness's own configuration folder for the proof,
// and the variable that points the harness at it. Codex gets a fresh folder
// holding a copy of this user's file login, removed when the proof ends. pi
// gets the folder CFO_NATIVE_COMPACT_PI_DIR names, which must not be this
// user's own and must sign its default provider in by OAuth: a subscription,
// never a metered key.
func (p *wakeProof) nativeConfig(t *testing.T) (string, string) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if p.cfo == "pi" {
		dir := os.Getenv("CFO_NATIVE_COMPACT_PI_DIR")
		if !filepath.IsAbs(dir) || strings.EqualFold(filepath.Clean(dir), filepath.Join(userHome, ".pi", "agent")) {
			t.Fatal("CFO_NATIVE_COMPACT_PI_DIR must name an absolute pi configuration folder other than this user's own")
		}
		var settings struct {
			Provider string `json:"defaultProvider"`
			Model    string `json:"defaultModel"`
		}
		var credentials map[string]struct {
			Type string `json:"type"`
		}
		if data, err := os.ReadFile(filepath.Join(dir, "settings.json")); err != nil || json.Unmarshal(data, &settings) != nil || settings.Provider == "" {
			t.Fatalf("%s names no default provider: %v", filepath.Join(dir, "settings.json"), err)
		}
		if data, err := os.ReadFile(filepath.Join(dir, "auth.json")); err != nil || json.Unmarshal(data, &credentials) != nil || credentials[settings.Provider].Type != "oauth" {
			t.Fatalf("pi's default provider %s is not signed in by OAuth in %s (%v), so the proof could spend a metered key", settings.Provider, dir, err)
		}
		p.say("pi runs on %s/%s, signed in by OAuth", settings.Provider, settings.Model)
		return dir, "PI_CODING_AGENT_DIR"
	}
	dir := filepath.Join(p.root, "codex")
	writeProofFile(t, filepath.Join(dir, "config.toml"), "check_for_update_on_startup = false\nmodel_reasoning_effort = \"low\"\n[projects."+strconv.Quote(p.project)+"]\ntrust_level = \"trusted\"\n")
	login, err := os.ReadFile(filepath.Join(userHome, ".codex", "auth.json"))
	if err != nil {
		t.Fatal("the proof needs an existing Codex file login")
	}
	var parsed struct {
		Key *string `json:"OPENAI_API_KEY"`
	}
	if json.Unmarshal(login, &parsed) != nil || parsed.Key != nil {
		t.Fatal("the Codex login holds an API key, so the proof could spend a metered key")
	}
	copied := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(copied, login, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// A scratch Codex that refreshed its login rotated the token this
		// user's own Codex holds; say so rather than discard it unseen.
		if now, err := os.ReadFile(copied); err == nil && !bytes.Equal(now, login) {
			t.Errorf("the scratch Codex rewrote its copy of the login, so ~/.codex/auth.json may hold a rotated token; %s is kept", copied)
			return
		}
		_ = os.Remove(copied)
	})
	return dir, "CODEX_HOME"
}

// expectCompactHooks checks the install gave the harness its compaction
// events: Codex's PreCompact and PostCompact hooks, and the pi extension's
// two compaction handlers.
func (p *wakeProof) expectCompactHooks(t *testing.T, configDir string) {
	if p.cfo == "pi" {
		var found []string
		_ = filepath.WalkDir(filepath.Join(configDir, "extensions"), func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte(`"session_before_compact"`)) && bytes.Contains(data, []byte(`"session_compact"`)) {
					found = append(found, path)
				}
			}
			return nil
		})
		if len(found) != 1 {
			t.Fatalf("%d pi extensions handle both compaction events, want one: %v", len(found), found)
		}
		p.say("pi extension %s handles session_before_compact and session_compact", found[0])
		return
	}
	data, err := os.ReadFile(filepath.Join(configDir, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreCompact", "PostCompact"} {
		if len(settings.Hooks[event]) != 1 {
			t.Fatalf("hooks.json holds %d %s groups, want one", len(settings.Hooks[event]), event)
		}
	}
	p.say("hooks.json defines PreCompact and PostCompact")
}

// startCompactCFO starts the CFO in native terminal cfo with the arguments a
// CFO of its harness starts with and the proof's instructions as its first
// prompt. Codex runs apart from the shared background server, which holds
// this user's own configuration, and runs the hooks just installed in its
// scratch folder for this invocation only, so no trust is recorded anywhere.
func (p *wakeProof) startCompactCFO(t *testing.T) host.Record {
	prompt := "You stand in for the CFO of a scratch Code Goblins home in an automated test. Use the shell only as told. First run the command cfo register then reply standing by and end your turn. After that, every time a line starting with cfo watcher wake reaches you, run cfo drain, then run the WAKE_ACK_REQUIRED command it prints exactly as printed, adding --ack-blocking only if it is refused, then reply with one short line naming each drained record by kind and key, and end your turn. Answer anything else in one short line without running a command. Never run cfo reap, and run no other command and change no file."
	var args []string
	var err error
	switch p.cfo {
	case "codex":
		args, err = spawn.NativeProgram("codex", "--no-daemon", "--no-alt-screen", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "-m", proofModel("codex"), prompt)
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
		if client, err := host.Dial(record); err == nil {
			_ = client.CloseTerminal()
			_ = client.Close()
		}
		if process, err := os.FindProcess(record.HostPID); err == nil {
			_ = process.Kill()
		}
	})
	return p.settle(t, record)
}

// dismissOptionalNotices closes, until ctx ends, each optional offer with a
// known Escape hint the CFO's terminal shows, as the board's send to the CFO
// does before it types. Codex 0.160 drew its Daybreak security offer under the
// composer once its first turn ended, live on 2026-10-08, and the typed wake
// does not type while it shows; each dismissal is logged, so the record says
// when the proof stood in for that.
func (p *wakeProof) dismissOptionalNotices(ctx context.Context, record host.Record) {
	screens, _ := harness.NativeScreens(harness.Kind(p.cfo))
	for {
		if screen, err := host.ReadScreen(record); err == nil {
			if dialog, shown := screens.Dialog(screen); shown && dialog.EscapeHint != "" {
				p.say("dismissing %s with Escape: %v", dialog.Name, spawn.AnswerDialog(ctx, record, dialog, screen))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// compactionEnded is when the harness's own session record says the
// compaction since began ended: the compacted line of the Codex rollout, or
// the compaction entry of the pi session.
func (p *wakeProof) compactionEnded(configDir string, since time.Time) (time.Time, error) {
	kind := `"type":"compacted"`
	if p.cfo == "pi" {
		kind = `"type":"compaction"`
	}
	var ended time.Time
	err := filepath.WalkDir(filepath.Join(configDir, "sessions"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		lines := bufio.NewScanner(file)
		lines.Buffer(make([]byte, 0, 1<<20), 64<<20)
		for lines.Scan() {
			if !bytes.Contains(lines.Bytes(), []byte(kind)) {
				continue
			}
			var line struct {
				Type      string    `json:"type"`
				Timestamp time.Time `json:"timestamp"`
			}
			if json.Unmarshal(lines.Bytes(), &line) == nil && strings.Contains(kind, `"`+line.Type+`"`) && line.Timestamp.After(since) && (ended.IsZero() || line.Timestamp.Before(ended)) {
				ended = line.Timestamp
			}
		}
		return lines.Err()
	})
	if err == nil && ended.IsZero() {
		err = fmt.Errorf("no %s line after %s under %s", kind, since.UTC().Format(time.RFC3339Nano), filepath.Join(configDir, "sessions"))
	}
	return ended, err
}

// userTemp is the temporary directory env names, as cfo serve started with
// it reads it: TMP first, then TEMP.
func userTemp(env []string) string {
	for _, variable := range []string{"TMP", "TEMP"} {
		for _, entry := range env {
			if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, variable) && value != "" {
				return filepath.Clean(value)
			}
		}
	}
	return filepath.Clean(os.TempDir())
}
