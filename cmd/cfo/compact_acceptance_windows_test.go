package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/layout"
)

// TestACompactingClaudeCFOIsHandedItsWholeDigest is the end-to-end proof that
// a real Claude Code CFO is handed its session-start digest whole, at startup
// and again after a compact: Claude Code's own transcript keeps what the hook
// printed and what the session was given, and the two must be the same. The
// scratch home carries context files several times the size Claude Code hands
// over whole, so a digest that printed them would arrive as a preview.
//
// It runs a real Claude Code session on the Overlord's subscription, so it
// runs only when asked:
//
//	CFO_COMPACT_REAL=1 CFO_COMPACT_BINARY=<cfo.exe built from the tree to prove>
//	CFO_COMPACT_PROJECT=<a path nothing occupies> CFO_COMPACT_RESULTS=<a directory for the record>
func TestACompactingClaudeCFOIsHandedItsWholeDigest(t *testing.T) {
	if os.Getenv("CFO_COMPACT_REAL") != "1" {
		t.Skip("set CFO_COMPACT_REAL=1 with CFO_COMPACT_BINARY, CFO_COMPACT_PROJECT and CFO_COMPACT_RESULTS to prove the digest with a real Claude Code CFO")
	}
	binary, project, results := os.Getenv("CFO_COMPACT_BINARY"), os.Getenv("CFO_COMPACT_PROJECT"), os.Getenv("CFO_COMPACT_RESULTS")
	if binary == "" || project == "" || results == "" {
		t.Fatal("CFO_COMPACT_BINARY, CFO_COMPACT_PROJECT and CFO_COMPACT_RESULTS are all required")
	}
	if root, state := home.Inherited(); root != "" || state != "" {
		t.Fatalf("this process inherited the fleet home %s (state %s); unset CFO_HOME and CFO_STATE_OVERRIDE first", root, state)
	}
	p := &wakeProof{binary: binary, project: project, root: results, cfo: "claude"}
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
	if err := os.MkdirAll(p.home.State, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProofFile(t, filepath.Join(p.home.Root, "AGENTS.md"), "# Scratch CFO home for the compact proof\n")
	writeProofFile(t, filepath.Join(p.home.Root, home.InstalledMarker), "")
	// Each context file alone is longer than Claude Code hands over whole.
	for _, name := range []string{"projects.md", "overlord.md", filepath.FromSlash(layout.MemoryIndex)} {
		writeProofFile(t, filepath.Join(p.home.Data, name), "# "+name+"\n"+strings.Repeat("- a standing line of "+name+" that a CFO must not be told it has read\n", 3*digest.Limit/70))
	}
	p.say("scratch home %s", p.home.Root)
	p.setUpProject(t)
	settings, err := json.Marshal(map[string]any{"hooks": map[string]any{
		"PreCompact": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": p.binary, "args": []string{"hook", "pre-compact"}}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeProofFile(t, filepath.Join(p.project, ".claude", "settings.local.json"), string(settings))
	p.env = p.environment()

	cfo := p.startCFO(t)
	p.cfoTerminal = &cfo
	p.say("CFO claude runs in native terminal cfo, host pid %d, harness pid %d", cfo.HostPID, cfo.ChildPID)
	screens, _ := harness.NativeScreens(harness.Claude)
	idle := func() bool {
		screen, err := host.ReadScreen(cfo)
		return err == nil && screens.IsReady(screen)
	}
	p.await(t, "the CFO to register and end its first turn", 5*time.Minute, func() bool {
		primary, live := livePrimaryFile(p.home.State)
		return live && primary == "claude" && idle()
	})

	session := p.lockSession(t)
	p.say("session %s", session)
	p.expectWholeDigest(t, session, "SessionStart:startup")
	checkpoint := filepath.Join(p.home.State, digest.CheckpointFile)
	if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
		t.Fatalf("a checkpoint exists before compaction: %v", err)
	}

	client, err := host.Dial(cfo)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	compactAt := time.Now()
	if err := client.Input([]byte("/compact")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if err := client.Input([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	p.say("typed /compact")
	hook := p.expectWholeDigest(t, session, "SessionStart:compact")
	info, err := os.Stat(checkpoint)
	if err != nil {
		t.Fatalf("PreCompact did not write the checkpoint: %v", err)
	}
	if hook.Recorded.IsZero() || info.ModTime().Before(compactAt) || info.ModTime().After(hook.Recorded) {
		t.Fatalf("checkpoint written %s, /compact typed %s, SessionStart:compact recorded %s", info.ModTime(), compactAt, hook.Recorded)
	}
	if !strings.Contains(hook.Content, checkpoint+" holds the checkpoint written before compaction") {
		t.Fatalf("the delivered compact digest does not name the fresh checkpoint:\n%s", hook.Content)
	}
	p.say("checkpoint written %s, before SessionStart:compact recorded %s; the delivered digest names it", info.ModTime().UTC().Format(time.RFC3339Nano), hook.Recorded.UTC().Format(time.RFC3339Nano))
}

// lockSession is the session the home's lock names, as the CFO's SessionStart
// hook recorded it.
func (p *wakeProof) lockSession(t *testing.T) string {
	data, err := os.ReadFile(filepath.Join(p.home.State, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	var held struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(data, &held); err != nil || held.Session == "" {
		t.Fatalf("the session lock %s names no session: %v", data, err)
	}
	return held.Session
}

// sessionStartHook is one SessionStart hook as Claude Code's transcript keeps
// it: what the hook printed, and what the session was given.
type sessionStartHook struct {
	Name     string    `json:"hookName"`
	Stdout   string    `json:"stdout"`
	Content  string    `json:"content"`
	Recorded time.Time `json:"-"`
}

// sessionStartHooks reads every SessionStart hook named name from the
// transcripts of session, where the Claude Code started with env keeps them:
// the CFO runs with the user's environment, never this test binary's, which
// names a scratch configuration folder of its own.
func sessionStartHooks(env []string, session, name string) ([]sessionStartHook, error) {
	config := ""
	for _, entry := range env {
		if key, value, found := strings.Cut(entry, "="); found && strings.EqualFold(key, "CLAUDE_CONFIG_DIR") {
			config = value
		}
	}
	if config == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		config = filepath.Join(userHome, ".claude")
	}
	transcripts, err := filepath.Glob(filepath.Join(config, "projects", "*", session+".jsonl"))
	if err != nil {
		return nil, err
	}
	var hooks []sessionStartHook
	for _, transcript := range transcripts {
		file, err := os.Open(transcript)
		if err != nil {
			return nil, err
		}
		records := json.NewDecoder(file)
		for {
			var record struct {
				Attachment json.RawMessage `json:"attachment"`
				Timestamp  time.Time       `json:"timestamp"`
			}
			// The end of the file, or a record still being written, ends
			// the read: the next look reads it whole.
			if records.Decode(&record) != nil {
				break
			}
			// Other attachments keep other shapes under the same names, and
			// are not hooks.
			var hook sessionStartHook
			if json.Unmarshal(record.Attachment, &hook) == nil && hook.Name == name {
				hook.Recorded = record.Timestamp
				hooks = append(hooks, hook)
			}
		}
		file.Close()
	}
	return hooks, nil
}

// expectWholeDigest waits for the SessionStart hook named name in session's
// transcript, and checks the session was given all the hook printed: a digest
// within what Claude Code hands over whole, that claims no file it did not
// print, and that names a long digest which exists and holds the context the
// brief leaves out.
func (p *wakeProof) expectWholeDigest(t *testing.T, session, name string) sessionStartHook {
	var hook sessionStartHook
	p.await(t, "the "+name+" hook in the CFO's transcript", 6*time.Minute, func() bool {
		hooks, err := sessionStartHooks(p.env, session, name)
		if err != nil || len(hooks) == 0 {
			return false
		}
		hook = hooks[len(hooks)-1]
		return true
	})
	printed, given := strings.TrimSpace(hook.Stdout), strings.TrimSpace(hook.Content)
	p.say("%s: the hook printed %d bytes, the session was given %d bytes", name, len(printed), len(given))
	long := filepath.Join(p.home.State, digest.FullDigestFile)
	info, statErr := os.Stat(long)
	if statErr == nil {
		p.say("%s: the long digest %s holds %d bytes", name, long, info.Size())
	}
	if given != printed {
		p.say("%s: the session was given:\n%s", name, given)
		t.Fatalf("%s: the hook printed %d bytes and the session was given %d other bytes, so Claude Code did not hand the digest over whole; see %s", name, len(printed), len(given), filepath.Join(p.root, "proof.log"))
	}
	if len(printed) > digest.Limit {
		t.Errorf("%s: the digest is %d bytes, over the %d Claude Code hands over whole", name, len(printed), digest.Limit)
	}
	for _, want := range []string{"== SESSION LOCK ==", "== WAKE QUEUE ==", "== SUPERVISION OPERATING INSTRUCTIONS ==", "== FLEET ==", "READ THIS NEXT: " + long, "PRINTED IN FULL: none", "== NEXT STEP =="} {
		if !strings.Contains(given, want) {
			t.Errorf("%s: the session was not given %q", name, want)
		}
	}
	if strings.Contains(given, "a standing line of") {
		t.Errorf("%s: the brief digest printed a context file", name)
	}
	if statErr != nil || info.Size() <= int64(digest.Limit) {
		t.Errorf("%s: the long digest %s is missing or no longer than the limit (%v), so the proof shows nothing", name, long, statErr)
	}
	if data, err := os.ReadFile(long); err != nil || !strings.Contains(string(data), "a standing line of overlord.md") {
		t.Errorf("%s: the long digest does not hold data\\overlord.md (%v)", name, err)
	}
	return hook
}
