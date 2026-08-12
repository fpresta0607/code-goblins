# code-goblins Plan 2: Claude Hooks and Watcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Port the six Claude Code hook commands and the resident watcher to `cfo.exe`, preserving upstream First Mate's supervision contracts on Windows-native primitives.

**Architecture:** A `cfo hook <name>` family reads Claude's hook JSON on stdin and honors the two distinct output contracts: PreToolUse denies emit a JSON envelope on stderr with exit 2, while Stop hooks block or rewake with plain stderr text and exit 2.
The watcher runs as a triage loop hosted in-process by the `stop-autoarm` hook (Claude's `asyncRewake` keeps that hook process alive for up to 8 hours), closing one cycle per actionable event.
Session custody keys on the harness ancestor process (Win32 ancestry walk) plus Claude's `session_id`, never on the disposable hook process itself.

**Tech Stack:** Go stdlib only (the Windows `syscall` package included; `golang.org/x/sys` stays banned), GitHub Actions `windows-latest`.

This is Plan 2 of the series (Plan 1 shipped `cmd/cfo`, `fsx`, `state`, `lock`, `wake`, `doctor`).
Plan 3 adds fleet operations (treehouse worktrees, Herdr backend, spawn/send/peek and pane-based staleness), Plan 4 adds delivery (PR flows, backlog, check sweeps), Plan 5 rebrands the instruction layer and ships install.ps1 and releases.

## Global Constraints

- Module path: `github.com/fpresta0607/code-goblins`; binary `cfo` (`cfo.exe` on Windows).
- Pure Go, no cgo, zero third-party dependencies; the stdlib `syscall` package is allowed, `golang.org/x/sys` is not.
- Windows is the only target; platform-specific files use the `_windows.go` suffix.
- No symlinks anywhere, in code or tests; upstream's symlink locks become create-exclusive files via the existing `internal/lock` pattern or `os.O_CREATE|os.O_EXCL`.
- Every state-file write is atomic via `fsx.AtomicWriteFile`; append-only logs use bounded-retry opens (the Task 4 Plan 1 pattern).
- All line-oriented parsers accept CRLF and LF equally.
- State lives under a home's `state/` subdirectory; functions take the state directory (or home directory, where stated) as their first parameter; no globals.
- INERT MEANS INERT: when a hook's scope predicate fails, the hook exits 0, prints nothing, and creates no file or directory anywhere. This keeps the dev repo (no `state/` dir) safe to develop in with hooks wired.
- Hook exit-code contract: PreToolUse deny is exit 2 with a one-line stderr JSON envelope `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"},"systemMessage":"..."}` and empty stdout; Stop block/rewake is exit 2 with plain stderr text and empty stdout; informational allow is exit 0 with a stdout JSON `{"systemMessage":"..."}`; SessionStart is the one event that emits PLAIN TEXT on stdout with exit 0, because Claude Code injects SessionStart stdout into the session context verbatim and a JSON wrapper would deliver the digest as one escaped blob; an unknown hook name is exit 0 with a one-line stderr diagnostic; every other outcome is exit 0 and silent. Transport failures (bad stdin, missing fields) fail open: exit 0, silent.
- Environment variables use the `CFO_` prefix (upstream `FM_*` names are not read); defaults carry upstream's exact numbers.
- Naming in all output: the human is the Supreme Overlord, the primary agent is the CFO, workers are Code Goblins. No em dash characters in any output or doc.
- Performance: `pretool-arm`, `pretool-cd` and `pretool-subagent` complete in under 150ms each on the target machine, measured in Task 13 step 4. `turnend-guard`'s fast exits (not primary, need vanished, healthy watcher) share that budget by construction rather than by measurement: they pay the same home resolution and IsPrimary gate and then return, so Task 13's timing loop does not time them separately. The dominant cost is `home.IsPrimary`, whose git probe measures 64ms as the two separate `rev-parse` spawns Task 1 shipped and 31.5ms once Task 13 collapses them into one; Task 13 registers three PreToolUse hooks on a Bash matcher, so a single Bash tool call pays that cost three times. `turnend-guard` adds up to SyncWaitMS (default 800ms) on top whenever it has to wait for the sibling auto-arm's proof, and `stop-autoarm` is unbounded by design because it hosts the watcher. The session-start digest completes in under 1s. The inert path stays cheap in any case: IsPrimary stats AGENTS.md and state/ before it ever shells out, so a dev checkout never pays the git probe.
- Commits follow `<type>(<scope>): <subject>`; commit after every green test cycle. Branch: `feat/cfo-hooks-watcher`.
- Working directory `C:\dev\code-goblins`; Go at `C:\Program Files\Go\bin\go.exe` (not on PATH; use the absolute path or prepend to `$env:PATH` per command).
- Never execute anything under `bin/` or `tests/` (legacy bash reference); never modify `C:\dev\firstmate`.
- Every task ends with an e2e verification step that builds the real `cfo.exe` and exercises the new surface with a real stdin payload or state fixture, not only unit tests.

## Timing and policy constants (single source: `internal/claudehook/env.go`, Task 2)

| Constant | Env override | Default |
|---|---|---|
| GuardGrace | CFO_GUARD_GRACE | 300s |
| Poll | CFO_POLL | 15s |
| Heartbeat | CFO_HEARTBEAT | 600s |
| HeartbeatMax | CFO_HEARTBEAT_MAX | 7200s |
| SignalGrace | CFO_SIGNAL_GRACE | 30s |
| SyncWaitMS | CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS | 800 |
| EpochFresh | CFO_CLAUDE_AUTOARM_EPOCH_FRESH | 15s |
| BlockBudget | CFO_CLAUDE_TURNEND_BLOCK_BUDGET | 3 |
| AutoarmAttempts | CFO_CLAUDE_AUTOARM_ATTEMPTS | 2 (clamped 1..3) |
| StatusTail | CFO_SESSION_START_STATUS_TAIL | 5 |
| QueuedLimit | CFO_SESSION_START_QUEUED_LIMIT | 20 |
| EventCapFailMax | CFO_EVENT_CAP_FAIL_MAX | 3 |

Deliberate v1 cuts inside this plan (spec section 9 and phasing): no AFK gate (`state/.afk` is ignored), no gate-agent refusal, no network stage in the digest, no `*.check.sh` sweeps, no pane/window staleness (needs Herdr, Plan 3), no procevent sources, no X-mode.
Each cut is marked NOT PORTED IN V1 in code comments at the point upstream had the behavior.

## File Structure

```
internal/home/home.go              home/state resolution, primary-scope predicate
internal/home/home_test.go
internal/claudehook/payload.go     stdin payload decode
internal/claudehook/emit.go        the three output emitters + exit codes
internal/claudehook/env.go         CFO_* env table with defaults
internal/claudehook/*_test.go
internal/proc/proc_windows.go      ancestry walk, exe names, creation times (extends nothing; new package)
internal/proc/proc_windows_test.go
internal/lock/lock.go              MODIFY: owner-acquire (harness pid + session id), then the named-lock family (Task 5)
internal/lock/lock_test.go         MODIFY: owner tests
internal/wake/wake.go              MODIFY: Kind whitelist, Key field, dedup presentation
internal/wake/episode.go           watcher-down recovery-generation marker
internal/wake/*_test.go
internal/guard/subagent.go         delegation-shape classifier (pure)
internal/guard/armcd.go            watcher-invocation and cd policies (pure)
internal/guard/*_test.go
internal/watch/watch.go            triage loop: signatures, signal scan, heartbeat, beat, singleton
internal/watch/notify_windows.go   ReadDirectoryChangesW wait with circuit breaker
internal/watch/*_test.go
internal/supervise/supervise.go    needed/healthy predicates, block budget, epoch ledger
internal/supervise/supervise_test.go
internal/digest/digest.go          session-start digest composition
internal/digest/digest_test.go
cmd/cfo/hook.go                    cfo hook <name> dispatch
cmd/cfo/hook_test.go
cmd/cfo/drain.go                   cfo drain presentation and acks
cmd/cfo/e2e_hooks_test.go          whole-family e2e proof (Task 13)
cmd/cfo/main.go                    MODIFY: hook, watch, drain, session-start cases + usage
cmd/cfo/main_test.go               MODIFY: dispatch rows
.claude/settings.json              MODIFY: wire the six hook commands (final task)
```

---

### Task 1: internal/home, home resolution and the primary-scope predicate

**Files:**
- Create: `internal/home/home.go`
- Test: `internal/home/home_test.go`

**Interfaces:**
- Consumes: nothing from other packages (uses `os`, `os/exec`, `path/filepath`, `strings`).
- Produces:
  - `home.Resolve() (Home, error)` where `type Home struct { Root, State, Data string }`.
    Resolution order: `CFO_HOME` env if set, else the current working directory.
    `State` is `Root\state`, `Data` is `Root\data`; `CFO_STATE_OVERRIDE` overrides `State` when set.
    Resolve never creates directories.
  - `home.IsPrimary(h Home) bool`: true only when ALL hold: `h.Root\AGENTS.md` exists as a regular file, `h.State` exists as a directory, and `git -C h.Root rev-parse --git-dir` equals `git -C h.Root rev-parse --git-common-dir` (plain checkout, not a linked worktree).
    Any git failure or missing prerequisite returns false; IsPrimary never creates anything and never errors.
  - Path comparisons inside the package are case-insensitive (`strings.EqualFold` on cleaned absolute paths).

Upstream contract being ported: `fm_primary_scope_matches` minus the `bin/` requirement (the bash tree is being deleted) and minus the secondmate marker (secondmates are cut from v1).
The `state/` existence requirement is the deliberate arming switch: a checkout without `state/` is a dev checkout and every hook stays inert there.

- [ ] **Step 1: Write the failing tests**

Create `internal/home/home_test.go` with these cases, each using `t.TempDir()` and `t.Setenv`:

```go
package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestResolveDefaultsToCwd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CFO_HOME", "")
	t.Chdir(dir)
	h, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if h.State != filepath.Join(h.Root, "state") || h.Data != filepath.Join(h.Root, "data") {
		t.Errorf("derived dirs wrong: %+v", h)
	}
}

func TestResolveHonorsEnvOverrides(t *testing.T) {
	root := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", stateDir)
	h, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if h.Root != root || h.State != stateDir {
		t.Errorf("overrides ignored: %+v", h)
	}
}

func TestIsPrimaryRequiresAllThree(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	h := Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if IsPrimary(h) {
		t.Error("primary without AGENTS.md or state/")
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsPrimary(h) {
		t.Error("primary without state/")
	}
	if err := os.Mkdir(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsPrimary(h) {
		t.Error("not primary with AGENTS.md + state/ + plain checkout")
	}
}

func TestIsPrimaryFalseInLinkedWorktree(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "c"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(wt, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wt, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := Home{Root: wt, State: filepath.Join(wt, "state")}
	if IsPrimary(h) {
		t.Error("a linked worktree must never be primary")
	}
}

func TestIsPrimaryFalseOutsideGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if IsPrimary(Home{Root: dir, State: filepath.Join(dir, "state")}) {
		t.Error("primary outside a git checkout")
	}
}

func TestIsPrimaryNeverCreates(t *testing.T) {
	dir := t.TempDir()
	IsPrimary(Home{Root: dir, State: filepath.Join(dir, "state")})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("IsPrimary created entries: %v", entries)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/home -v`
Expected: FAIL to build with `undefined: Resolve`.

- [ ] **Step 3: Implement**

Create `internal/home/home.go`:

```go
// Package home resolves the CFO home directory and decides whether a
// directory is a genuine primary fleet home. A home without a state/
// directory is a dev checkout: every hook must stay inert there.
package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Home names the three directories everything else keys on.
type Home struct {
	Root  string
	State string
	Data  string
}

// Resolve returns the home from CFO_HOME or the working directory.
// It never creates directories.
func Resolve() (Home, error) {
	root := os.Getenv("CFO_HOME")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Home{}, err
		}
		root = wd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Home{}, err
	}
	h := Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if s := os.Getenv("CFO_STATE_OVERRIDE"); s != "" {
		h.State = s
	}
	return h, nil
}

// IsPrimary reports whether h is a genuine primary home: AGENTS.md present,
// state/ present, and a plain (non-worktree) git checkout. It never creates
// anything; any failure to confirm is false, never an error.
func IsPrimary(h Home) bool {
	if fi, err := os.Stat(filepath.Join(h.Root, "AGENTS.md")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if fi, err := os.Stat(h.State); err != nil || !fi.IsDir() {
		return false
	}
	gitDir, err := gitPath(h.Root, "--git-dir")
	if err != nil {
		return false
	}
	commonDir, err := gitPath(h.Root, "--git-common-dir")
	if err != nil {
		return false
	}
	return strings.EqualFold(gitDir, commonDir)
}

func gitPath(root, flag string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", flag).Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/home -v`
Expected: PASS (all six tests).

- [ ] **Step 5: Full-suite check and commit**

Run: `go vet ./...` then `go test ./...`
Expected: clean, all packages pass.

```powershell
git add internal/home
git commit -m "feat(home): home resolution and primary-scope predicate"
```

---

### Task 2: internal/claudehook, payload decode, output emitters, env table

**Files:**
- Create: `internal/claudehook/payload.go`
- Create: `internal/claudehook/emit.go`
- Create: `internal/claudehook/env.go`
- Test: `internal/claudehook/payload_test.go`
- Test: `internal/claudehook/emit_test.go`
- Test: `internal/claudehook/env_test.go`

**Interfaces:**
- Consumes: nothing from other packages.
- Produces:
  - `type claudehook.Payload struct { SessionID string; Source string; ToolName string; Command string; StopHookActive bool }` decoded by `claudehook.ReadPayload(r io.Reader) (Payload, bool)`.
    JSON field mapping: `session_id`, `source`, `tool_name`, `tool_input.command`, and `stopHookActive` with `stop_hook_active` as fallback (camelCase wins when both present).
    The bool is false on empty input, unreadable input, or JSON that is not an object: transport failures fail open, so callers exit 0.
  - `claudehook.DenyPreTool(stderr io.Writer, message string) int` returns 2 after writing exactly one line to stderr: the PreToolUse envelope with `message` JSON-escaped into `systemMessage`. Stdout is never touched.
  - `claudehook.BlockStop(stderr io.Writer, banner string) int` returns 2 after writing the plain-text banner to stderr.
  - `claudehook.InfoAllow(stdout io.Writer, message string) int` returns 0 after writing `{"systemMessage":"..."}` plus newline to stdout.
  - `claudehook.Seconds(name string, def int) time.Duration` and `claudehook.Int(name string, def, min, max int)` reading `CFO_*` env with defaults from the constants table; invalid values fall back to the default.

- [ ] **Step 1: Write the failing tests**

`payload_test.go` table cases (full assertions on every field):
1. Claude PreToolUse payload: `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"echo hi"}}` decodes to all fields, ok true.
2. SessionStart payload: `{"session_id":"s2","source":"startup"}` decodes Source, ok true.
3. Stop payload camelCase wins: `{"session_id":"s3","stopHookActive":true,"stop_hook_active":false}` gives StopHookActive true.
4. Stop payload snake fallback: `{"stop_hook_active":true}` gives StopHookActive true.
5. Empty input: ok false.
6. Garbage input `not json`: ok false.
7. JSON array `[1]`: ok false.
8. Missing tool_input: Command empty, ok true.

`emit_test.go`:
1. `DenyPreTool` returns 2; stderr parses as JSON with `hookSpecificOutput.hookEventName == "PreToolUse"`, `hookSpecificOutput.permissionDecision == "deny"`, and `systemMessage` equal to the input message; output is exactly one line.
2. `DenyPreTool` escapes quotes and newlines in the message (round-trips through `encoding/json`).
3. `BlockStop` returns 2 and writes the banner verbatim to stderr.
4. `InfoAllow` returns 0 and stdout parses as `{"systemMessage": msg}`.

`env_test.go`:
1. `Seconds("CFO_GUARD_GRACE", 300)` returns 300s when unset, 7s when set to `7`, 300s when set to `bogus`.
2. `Int("CFO_CLAUDE_AUTOARM_ATTEMPTS", 2, 1, 3)` clamps `9` to 3 and `0` to 1, returns 2 on unset or garbage.

Write these as real Go test functions with concrete literals; each case asserts the exact expected value.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/claudehook -v`
Expected: FAIL to build with `undefined: ReadPayload`.

- [ ] **Step 3: Implement the three files**

`payload.go`: decode into a struct with `json.RawMessage` for tool_input, then a second unmarshal for `command`; use pointer fields `*bool` for the two stop-hook spellings to distinguish absent from false, camelCase pointer consulted first.
`emit.go`: build the deny envelope with `json.Marshal` over an anonymous struct (never string concatenation), one `fmt.Fprintln` per emitter.
`env.go`: two lookup helpers over `os.Getenv` with `strconv.Atoi` and range clamping.
Every exported identifier carries a doc comment naming the upstream contract it ports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/claudehook -v`
Expected: PASS.

- [ ] **Step 5: Full-suite check and commit**

Run: `go vet ./...` then `go test ./...`

```powershell
git add internal/claudehook
git commit -m "feat(claudehook): hook payload decode and output contracts"
```

---

### Task 3: internal/proc, Windows process ancestry

**Files:**
- Create: `internal/proc/proc_windows.go`
- Test: `internal/proc/proc_windows_test.go`

**Interfaces:**
- Consumes: nothing from other packages (stdlib `syscall` only).
- Produces:
  - `proc.Ancestry(pid int, maxHops int) ([]proc.Entry, error)` where `type Entry struct { PID int; ParentPID int; ExeBase string; Start time.Time }`.
    Walks parent links via `CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS)` + `Process32First/Next` (all present in stdlib `syscall`), resolving creation times per hop with the Plan 1 `processStart` technique (`OpenProcess` + `GetProcessTimes`).
    A parent whose creation time is LATER than the child's is treated as PID reuse and terminates the walk (upstream's ancestry integrity rule).
  - `proc.FindAncestor(pid int, maxHops int, names ...string) (Entry, bool)`: first ancestor (including self) whose lowercased ExeBase, with `.exe` stripped, equals any of names.
  - `proc.Self() int` returns `os.Getpid()`.
- The harness names the callers will pass: `"claude"`, `"node"` (Claude Code ships as a node-hosted CLI on Windows; both names are checked by callers, in that order).

- [ ] **Step 1: Write the failing tests**

`proc_windows_test.go`:

```go
package proc

import (
	"os"
	"os/exec"
	"testing"
)

func TestAncestryIncludesSelfAndParent(t *testing.T) {
	entries, err := Ancestry(os.Getpid(), 16)
	if err != nil {
		t.Fatalf("Ancestry: %v", err)
	}
	if len(entries) < 1 || entries[0].PID != os.Getpid() {
		t.Fatalf("first entry must be self, got %+v", entries)
	}
	if entries[0].ExeBase == "" || entries[0].Start.IsZero() {
		t.Errorf("self entry incomplete: %+v", entries[0])
	}
	if len(entries) >= 2 && entries[1].PID != entries[0].ParentPID {
		t.Errorf("chain broken: %+v", entries[:2])
	}
}

func TestFindAncestorFindsSelfByName(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := baseNoExe(self)
	e, ok := FindAncestor(os.Getpid(), 16, base)
	if !ok || e.PID != os.Getpid() {
		t.Errorf("FindAncestor(%q) = %+v %v, want self", base, e, ok)
	}
}

func TestFindAncestorMissReturnsFalse(t *testing.T) {
	if _, ok := FindAncestor(os.Getpid(), 16, "no-such-process-name-xyz"); ok {
		t.Error("found an ancestor that cannot exist")
	}
}

func TestAncestryOfChildProcessSeesUs(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 3 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	entries, err := Ancestry(cmd.Process.Pid, 16)
	if err != nil {
		t.Fatalf("Ancestry(child): %v", err)
	}
	found := false
	for _, e := range entries {
		if e.PID == os.Getpid() {
			found = true
		}
	}
	if !found {
		t.Errorf("test process missing from child ancestry: %+v", entries)
	}
}
```

`baseNoExe` is an exported-in-test helper the implementation also uses internally: lowercase base name with a trailing `.exe` removed; export it as `BaseNoExe` if sharing is cleaner.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proc -v`
Expected: FAIL to build with `undefined: Ancestry`.

- [ ] **Step 3: Implement**

`proc_windows.go` implementation notes (write real code, not this outline, but preserve these decisions):
- One snapshot per `Ancestry` call: `syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)`, iterate `Process32First/Process32Next` into a `map[uint32]syscall.ProcessEntry32` keyed by PID, and remember each entry's `ParentProcessID` and `ExeFile` (UTF-16 fixed array; convert with `syscall.UTF16ToString(pe.ExeFile[:])`).
- Walk from pid up to maxHops, appending an Entry per hop; resolve `Start` via a package-local `processStart(pid)` identical in technique to `internal/lock/proc_windows.go` (do NOT import internal/lock; the two-line syscall pair is cheaper than an export).
- Stop the walk when: pid missing from the snapshot, ParentPID is 0, hop limit reached, the parent's creation time is after the child's (PID reuse), or the hop's creation time cannot be resolved (OpenProcess/GetProcessTimes failure).
- `FindAncestor` lowercases with `strings.ToLower` and strips `.exe` before comparing.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/proc -v`
Expected: PASS (four tests).

- [ ] **Step 5: Full-suite check and commit**

Run: `go vet ./...` then `go test ./...`

```powershell
git add internal/proc
git commit -m "feat(proc): windows process ancestry walk"
```

---

### Task 4: internal/lock owner-acquire, custody for harness plus session

**Files:**
- Modify: `internal/lock/lock.go`
- Modify: `internal/lock/lock_test.go`

**Interfaces:**
- Consumes: `proc.Ancestry` semantics indirectly (callers pass a pid they resolved; lock does not import proc).
- Produces:
  - `Info` gains two fields: `Session string` with JSON tag `session` and `OwnerPID int` with JSON tag `owner_pid` (additive; existing files without them unmarshal to zero values).
  - `lock.AcquireOwner(dir string, ownerPID int, session string) (*Info, error)`: like `Acquire`, but the recorded identity is `ownerPID` and ITS creation time (queried via the existing unexported `processStart`), with `Session` recorded verbatim.
    `Info.PID` stays the recorded owner pid (for backward shape compatibility `OwnerPID` mirrors it; both are written).
    Self-idempotence: a live holder matching `ownerPID` + its start time + hostname returns success regardless of `Session` (a resumed Claude session keeps custody after `/clear`).
  - `lock.HeldBy(dir string, ownerPID int) bool`: true when the current holder record names `ownerPID`, matches its live creation time, and the hostname matches; false on any read error.
    An unverifiable owner (`statusUnknown`) reads as held WITHOUT the Start comparison, the same fail-closed branch as the zero-Start deviation below, so HeldBy proves custody is not demonstrably lost, not that the recorded identity is the same process.
  - Existing `Acquire(dir)` becomes a one-line wrapper: `AcquireOwner(dir, os.Getpid(), "")`.
  - `lock.ErrOwnerDead`, an exported sentinel (SANCTIONED DEVIATION, already shipped and recorded in the ledger): `AcquireOwner` refuses a verifiably dead `ownerPID` and returns it before creating any file, so a caller passing a pid that has already exited never takes custody and never leaves a record behind. A merely unverifiable owner (`statusUnknown`) still proceeds and acquires. Tasks 11 and 12 name this branch explicitly.
  - A `statusUnknown` owner records a ZERO `Start` time (SANCTIONED DEVIATION, already shipped), so a recorded `Start` is not proof of identity and `(*Info).Alive()` fails closed by returning true. Every liveness decision in Tasks 8 through 11 therefore requires a fresh mtime beacon as well and never rests on the lock record alone.
- Release stays keyed to the calling process and is NOT extended to owners in this plan: hooks never release the session lock (the lock dies with the harness process); `Release` keeps its Plan 1 semantics for tests and future non-hook callers. NOT PORTED IN V1: explicit owner release.

- [ ] **Step 1: Write the failing tests**

Add to `internal/lock/lock_test.go` (keep every existing test unchanged):

```go
func TestAcquireOwnerRecordsForeignPID(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	info, err := AcquireOwner(dir, cmd.Process.Pid, "sess-1")
	if err != nil {
		t.Fatalf("AcquireOwner: %v", err)
	}
	if info.PID != cmd.Process.Pid || info.OwnerPID != cmd.Process.Pid || info.Session != "sess-1" {
		t.Errorf("recorded identity wrong: %+v", info)
	}
	if !HeldBy(dir, cmd.Process.Pid) {
		t.Error("HeldBy(owner) = false for live owner")
	}
	if HeldBy(dir, os.Getpid()) {
		t.Error("HeldBy(non-owner) = true")
	}
}

func TestAcquireOwnerIdempotentAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	if _, err := AcquireOwner(dir, cmd.Process.Pid, "sess-1"); err != nil {
		t.Fatal(err)
	}
	info, err := AcquireOwner(dir, cmd.Process.Pid, "sess-2")
	if err != nil {
		t.Fatalf("same-owner reacquire with new session: %v", err)
	}
	if info.PID != cmd.Process.Pid {
		t.Errorf("identity changed: %+v", info)
	}
}

func TestAcquireOwnerContendedByLiveForeignOwner(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	if _, err := AcquireOwner(dir, cmd.Process.Pid, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireOwner(dir, os.Getpid(), "other"); !errors.Is(err, ErrHeld) {
		t.Errorf("err = %v, want ErrHeld while foreign owner lives", err)
	}
}

func TestAcquireOwnerStealsFromDeadOwner(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	stale := &Info{PID: cmd.ProcessState.Pid(), OwnerPID: cmd.ProcessState.Pid(), Start: time.Now().Add(-time.Hour), Hostname: localHostnameForTest(t), Session: "dead"}
	if err := writeInfo(filepath.Join(dir, ".lock"), stale); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireOwner(dir, os.Getpid(), "new"); err != nil {
		t.Fatalf("steal from dead owner: %v", err)
	}
}

func TestAcquireOwnerRefusesDeadOwner(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireOwner(dir, cmd.ProcessState.Pid(), "s"); !errors.Is(err, ErrOwnerDead) {
		t.Errorf("err = %v, want ErrOwnerDead", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused acquire left a record behind")
	}
}

func TestHeldByRejectsForeignHostname(t *testing.T) {
	// A record naming this process's own PID and live Start (so the liveness
	// conjunct alone would pass) but a foreign Hostname must not read as held.
	dir := t.TempDir()
	start, _ := processStart(os.Getpid())
	foreign := &Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Start: start, Hostname: "some-other-host"}
	if err := writeInfo(filepath.Join(dir, ".lock"), foreign); err != nil {
		t.Fatal(err)
	}
	if HeldBy(dir, os.Getpid()) {
		t.Error("HeldBy = true for a foreign hostname")
	}
}

func TestHeldByRejectsDeadOwner(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	stale := &Info{PID: cmd.ProcessState.Pid(), OwnerPID: cmd.ProcessState.Pid(), Start: time.Now().Add(-time.Hour), Hostname: localHostnameForTest(t)}
	if err := writeInfo(filepath.Join(dir, ".lock"), stale); err != nil {
		t.Fatal(err)
	}
	if HeldBy(dir, cmd.ProcessState.Pid()) {
		t.Error("HeldBy = true for a verifiably dead owner")
	}
}
```

`localHostnameForTest` mirrors the existing tests' hostname helper; reuse whatever helper name the file already has.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/lock -v`
Expected: FAIL to build with `undefined: AcquireOwner`.

- [ ] **Step 3: Implement**

Refactor inside `internal/lock/lock.go` only:
- `selfInfo()` becomes `ownerInfo(pid int, session string) (*Info, processStatus)` (self is `ownerInfo(os.Getpid(), "")`, discarding the status); it queries `processStart(pid)` and records `PID: pid, OwnerPID: pid, Session: session`. `AcquireOwner` returns `ErrOwnerDead` when that status is `statusDead`, before calling `acquire`.
- The Acquire loop body is extracted unchanged into `acquire(dir string, self *Info) (*Info, error)`; `Acquire` and `AcquireOwner` both call it.
- The idempotent self-match inside the loop compares `holder.PID == self.PID && holder.Start.Equal(self.Start) && holder.Hostname == self.Hostname` exactly as today (Session deliberately excluded; add the comment "a resumed session keeps custody").
- `HeldBy` reads the record, requires hostname match, PID match, and liveness through `holder.Alive()`, which applies the same one-second Start tolerance for a verifiable process and fails closed to true for a `statusUnknown` one, exactly as the Produces bullet states.
- Doc comments updated: the lock records CUSTODY of a long-lived owner process (the harness), not the caller.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/lock -v`
Expected: PASS (all prior tests plus the seven new ones enumerated in the block above).

- [ ] **Step 5: Full-suite check and commit**

Run: `go vet ./...` then `go test ./...`

```powershell
git add internal/lock
git commit -m "feat(lock): owner-acquire custody for harness process and session"
```

---

### Task 5: internal/wake parity upgrade and cfo drain

**Files:**
- Modify: `internal/wake/wake.go`
- Create: `internal/wake/episode.go`
- Modify: `internal/wake/wake_test.go`
- Create: `internal/wake/episode_test.go`
- Create: `cmd/cfo/drain.go`
- Modify: `cmd/cfo/main.go` (add `drain` case and usage line)
- Modify: `cmd/cfo/main_test.go` (dispatch row)
- Modify: `internal/lock/lock.go` (the named-lock family)
- Modify: `internal/lock/lock_test.go` (named-lock tests)

**Interfaces:**
- Consumes: `fsx.ReadLines`, `fsx.AtomicWriteFile` (existing), and `lock.AcquireNamedOwner`/`lock.ReleaseNamed`, which wake.go now imports for the `.wake-queue.lock` serialization invariant below.
- Produces:
  - The named-lock family in `internal/lock`, landed here because Task 5 is the first consumer: `lock.AcquireNamedOwner(dir, name string, ownerPID int, session string) (*Info, error)`, `lock.ReadNamed(dir, name string) (*Info, error)`, `lock.HeldByNamed(dir, name string, ownerPID int) bool` and `lock.ReleaseNamed(dir, name string) error`. Each is its Plan 1 counterpart with the lock file at `dir\<name>` instead of `dir\.lock`, and `AcquireOwner`, `Read`, `HeldBy` and `Release` become one-line delegations passing `.lock`. Thread `name` through the unexported `acquire` so the joined path is used by `writeInfo`, the read-back verification, the holder read, the dead-holder re-read and `os.Remove`; this is the whole change and it is not a four-line edit. Everything Task 4 shipped is inherited unchanged by every named lock, including the `ErrOwnerDead` refusal of a verifiably dead ownerPID and the session-blind idempotent self-match.
  - Serialization invariant: every mutation of `state/.wake-queue`, `state/.wake-ack` and `state/.watcher-down` - that is `Append`, `AckThrough`, `PublishEpisode` and `AckEpisode` - performs its read-modify-write while holding `state/.wake-queue.lock`, taken with `lock.AcquireNamedOwner(dir, ".wake-queue.lock", os.Getpid(), "wake")` and released with `lock.ReleaseNamed`. The acquire is retried on `lock.ErrHeld` up to 10 times at 50ms, and a still-held lock after 500ms is returned to the caller as an error rather than swallowed; a dead holder is stolen by the lock package itself, so a process killed inside the critical section cannot wedge the home. Read-only paths (`Pending`, `ReadEpisode`, and the drain's presentation) take no lock and create nothing, which keeps INERT MEANS INERT intact. Delete the `ponytail: single-writer sequencing` comment above `Append` in the same commit: it is the precondition this bullet retires.
  - `wake.Record` gains `Key string` with JSON tag `key` (additive; old lines unmarshal with empty Key).
  - `wake.Append(dir, kind, key, detail string) (Record, error)` SIGNATURE CHANGE from Plan 1's three-argument form; `kind` must be one of `signal`, `stale`, `check`, `heartbeat` or Append returns an error naming the whitelist. All existing wake tests are updated mechanically to pass a key (use the kind as key where the old tests passed only kind+detail).
  - `wake.Deduped(records []Record) []Record`: presentation fold, last-write-wins per `(kind,key)` preserving first-seen order of surviving buckets; ALL `heartbeat` records collapse into one bucket regardless of key.
  - `wake.PublishEpisode(dir string) (int, error)`: increments the generation and atomically writes `state/.watcher-down` as one line `pending:<gen>`. NOT PORTED IN V1: upstream's second phase (`handling`, its `fm_recovery_marker_begin_handling`) is cut, because nothing in Plan 2 transitions an episode between presentation and acknowledgement and a written-but-never-produced value has no place in the schema.
  - `wake.ReadEpisode(dir string) (Episode, error)` with `type Episode struct { Pending bool; Gen int }`; a missing file returns a zero Episode and a nil error, and so does any line that does not split into exactly two colon-separated fields whose second field parses as an integer, so a truncated or hand-edited marker degrades instead of panicking on an out-of-range index inside a Stop hook.
  - `wake.AckEpisode(dir string, gen int) error`: retires a pending episode whose generation matches by rewriting the line as `acked:<gen>`; a generation mismatch returns `wake.ErrGenerationMismatch` (callers treat it as re-drain, not failure).
  - `cfo drain [--ack-through <seq>] [--recovery-generation <gen>]`: with no flags, prints the deduped pending queue and any pending episode, then the exact ack command line; with flags, performs the acks. `--ack-through` alone acks sequences and never touches the episode; `--recovery-generation` alone acks the episode only when the queue is already empty. Exit 0 in every non-error case, including generation mismatch (which prints `recovery generation moved, re-run: cfo drain`). A write error on the output stream is the one nonzero exit `runDrain` produces, and Task 12's extraction of the renderer into `wake.Render` preserves it as that function's error return.

Drain output format, exact:

```
WAKE QUEUE: 3 pending
  2  signal  g1.status: signal:g1.status
  5  stale   w1: stale: w1 (idle 300s)
  7  heartbeat  heartbeat
RECOVERY EPISODE: pending, generation 4
WAKE_ACK_REQUIRED: cfo drain --ack-through 7 --recovery-generation 4
```

Each record row is `  %d  %-6s  %s: %s` over seq, kind, key and detail, with the `%s: ` key segment omitted when key equals kind, which is why the heartbeat row shows one bare word.
The header count is the number of RENDERED (deduped) rows, not the raw pending count; records collapsed by the fold are still retired by the ack-through sequence, which is always the highest raw pending sequence.
There are exactly three output shapes. Queue empty and no episode pending prints exactly `WAKE QUEUE: empty` and nothing else. Queue empty with an episode pending prints `WAKE QUEUE: 0 pending`, the RECOVERY EPISODE line, and `WAKE_ACK_REQUIRED: cfo drain --ack-through 0 --recovery-generation <gen>`, which is reachable because Task 8 publishes an episode on every return path except lock-lost, including an error return that appended no record. Any non-empty queue prints the full listing above.
Flag presence is detected with `FlagSet.Visit`, never by value, because `--ack-through 0` is a legitimate argument that a value test cannot distinguish from an absent flag, and an implementer branching on `*ackThrough > 0` leaves the third shape's episode permanently unretired.
Ack ordering is fixed: apply `AckThrough` first, then call `AckEpisode` only when the resulting queue is empty, so a partial ack can never retire an episode whose records are still queued. On `wake.ErrGenerationMismatch` the sequence ack is KEPT (it is idempotent and forward-only), stdout gets `recovery generation moved, re-run: cfo drain`, and the exit code is 0.
Detail text conventions (`signal:<paths>`, `stale: <window> (...)`, `check: <script>: <out>`, `heartbeat`) are the crew-facing contract; the watcher tasks emit them verbatim.

- [ ] **Step 1: Write the failing tests**

Update `wake_test.go`: adapt every existing call to the four-argument Append, then add:
1. `TestAppendRejectsUnknownKind`: `Append(dir, "bogus", "k", "d")` errors; the error text contains all four legal kinds.
2. `TestDedupedLastWriteWinsPerKindKey`: append `signal/a`, `signal/b`, `signal/a` (later detail); Deduped returns 2 records, the `a` bucket carrying the later detail and seq.
3. `TestDedupedCollapsesHeartbeats`: three heartbeats with different keys collapse to one record (the latest).
4. `TestDedupedKeepsBothBucketsAcrossCycles`: append `signal/a.status` and `signal/b.status` with the same detail (one cycle), then `signal/a.status` alone with a later detail; Deduped returns two records, the `a.status` bucket carrying the later detail and the `b.status` bucket surviving untouched. This is the fold behavior that one-record-per-cycle keying would break.

Create `episode_test.go`:
1. `TestPublishIncrementsGeneration`: two publishes yield gens 1 then 2; file holds `pending:2`.
2. `TestAckMatchingGeneration`: publish then Ack(gen) succeeds; ReadEpisode reports Pending false and the file holds `acked:1`.
3. `TestAckMismatchReturnsSentinel`: publish gen 1, publish gen 2, `AckEpisode(dir, 1)` returns ErrGenerationMismatch and the file still says `pending:2`.
4. `TestReadEpisodeMissingFileIsZero`: empty state dir reads as zero Episode, nil error.
5. `TestReadEpisodeToleratesMalformedLines`: an empty file, a file holding `pending`, and a file holding `pending:notanumber` each read as a zero Episode with a nil error and no panic.

Add to `internal/lock/lock_test.go`:
1. `TestAcquireNamedOwnerDistinctFiles` (lock package): `AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), "watch")` coexists with `Acquire(dir)`'s `.lock` in the same directory, and `HeldByNamed(dir, ".watch.lock", os.Getpid())` is true while `HeldByNamed(dir, ".lock", os.Getpid())` is also true for the plain lock. The predicates alone prove nothing, because an implementation that ignores `name` and always keys on `.lock` satisfies both (the shipped acquire loop is idempotent for the same pid), so the discriminating assertion is on disk: `os.Stat(filepath.Join(dir, ".watch.lock"))` and `os.Stat(filepath.Join(dir, ".lock"))` must BOTH succeed and must be two different files (identical content is fine).
2. `TestReleaseNamed` (lock package): `AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), "")`, then `ReleaseNamed(dir, ".watch.lock")`; `os.Stat(filepath.Join(dir, ".watch.lock"))` must return an error satisfying `errors.Is(err, os.ErrNotExist)`. Asserting a successful reacquire instead would pass against a total no-op, because the idempotent self-match returns success for a record that still names this process.

Add a `main_test.go` dispatch row: `{name: "drain empty queue", args: []string{"drain"}, wantExit: 0, wantStdout: "WAKE QUEUE: empty"}` with the exact string, not the `WAKE QUEUE` substring, which matches both the empty and the populated rendering and would pass even if CFO_HOME were ignored entirely (drain resolves the home from cwd; the test harness sets `CFO_HOME` to a `t.TempDir()` with a `state/` subdir via `t.Setenv` before calling `run`; add that setup support to the table struct as an optional `env map[string]string` field).
Add a `runDrain` unit test in `cmd/cfo` driving the renderer directly against an EXPLICITLY built state dir, because the sample block's numbers are a live home's numbers and not what three appends into a fresh dir produce: pre-seed `state\.wake-ack` with `1` so the lowest pending sequence is 2, write `state\.wake-queue` as three `wake.Record` values marshaled with `encoding/json` carrying seqs 2, 5, 7 (`signal/g1.status`, `stale/w1`, `heartbeat/heartbeat`, the gaps standing for records already acked away), and call `PublishEpisode` four times so the generation is 4. Assert the output LINE BY LINE against the exact block above, including the WAKE_ACK_REQUIRED line carrying the correct maximum sequence and generation. Add three more rows over fresh copies of that same fixture: `drain --ack-through 7 --recovery-generation 4` empties the queue, retires the episode and exits 0; a stale generation (`--ack-through 7 --recovery-generation 3`) exits 0 with `recovery generation moved` on stdout, the queue still emptied and the episode still reading `pending:4`; and a partial ack (`--ack-through 5 --recovery-generation 4`) leaves the episode PENDING because seq 7 is still queued after the ack.
Two more rows cover the single-flag semantics, which nothing else exercises: `drain --ack-through 7` alone retires every queue row and leaves the episode PENDING, and a following bare `drain` renders the second output shape (`WAKE QUEUE: 0 pending`, the RECOVERY EPISODE line, and a WAKE_ACK_REQUIRED line carrying `--ack-through 0 --recovery-generation 4`); then `drain --recovery-generation 4` alone, run against that same dir, acks the episode because the queue is already empty.
Add a fourth row for the third output shape: a published episode with no records renders `WAKE QUEUE: 0 pending` plus `--ack-through 0` in the WAKE_ACK_REQUIRED line, and running that exact printed command retires the episode.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/wake ./internal/lock ./cmd/cfo -v`
Expected: FAIL to build (signature change plus undefined episode and named-lock functions).

- [ ] **Step 3: Implement**

`lock.go`: thread `name` through the unexported `acquire` and give each of the four named entry points a doc comment naming the file it keys on; `Acquire`, `AcquireOwner`, `Read`, `HeldBy` and `Release` become one-line delegations passing `.lock`.
`wake.go`: add the whitelist check at the top of Append (`var kinds = map[string]bool{...}`); add Key to Record and to the append path; `Deduped` builds a `map[string]int` from bucket key (`kind+"\x00"+key`, with plain `heartbeat` as the bucket key for every heartbeat) to the index in an output slice, overwriting in place.
`episode.go`: one-line file `state/.watcher-down` parsed with `strings.Cut(line, ":")` into status and generation, rejecting anything that is not exactly two fields with an integer generation; generation state lives in that same file (the recorded gen is the current one, and Publish writes recorded+1); writes go through `fsx.AtomicWriteFile` under the `state/.wake-queue.lock` invariant from the Produces list.
`cmd/cfo/drain.go`: `runDrain(h home.Home, args []string, stdout, stderr io.Writer) int` using the `flag` package with a custom FlagSet; wire `case "drain":` in main.go after resolving home (missing `state/` dir prints `WAKE QUEUE: empty` and exits 0; drain never creates directories).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/wake ./internal/lock ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 5: E2E verification**

```powershell
& "C:\Program Files\Go\bin\go.exe" build ./cmd/cfo
$env:CFO_HOME = "$env:TEMP\cfo-e2e-drain"; New-Item -ItemType Directory -Force "$env:CFO_HOME\state" | Out-Null
.\cfo.exe drain
```
Expected: stdout is exactly `WAKE QUEUE: empty` and `$LASTEXITCODE` is 0; assert the exit code explicitly, since a pipeline that prints the right line while exiting non-zero would otherwise pass by eye.
Then remove `$env:CFO_HOME` from the environment, delete `.\cfo.exe`, and `Remove-Item -Recurse -Force $env:TEMP\cfo-e2e-drain` so a second run starts from a clean fixture.

- [ ] **Step 6: Full-suite check and commit**

Run: `go vet ./...` then `go test ./...`

```powershell
git add internal/wake internal/lock cmd/cfo
git commit -m "feat(wake): named locks, kind whitelist, dedup, episodes, cfo drain"
```

---

### Task 6: pretool-subagent guard and the cfo hook dispatcher

**Files:**
- Create: `internal/guard/subagent.go`
- Test: `internal/guard/subagent_test.go`
- Create: `cmd/cfo/hook.go`
- Test: `cmd/cfo/hook_test.go`
- Modify: `cmd/cfo/main.go` (add `hook` case and usage line)

**Interfaces:**
- Consumes: `claudehook.ReadPayload`, `claudehook.DenyPreTool`, `home.Resolve`, `home.IsPrimary`.
- Produces:
  - `guard.ClassifySubagent(tool string) (stem string, deny bool)`: MCP tools (`mcp__` prefix) never deny; the name is lowercased and stripped to `[a-z0-9]`; exact-name allowlists pass first (observe-only: `taskoutput taskstop taskget tasklist cronlist bashoutput killshell`; plan-only: `taskcreate taskupdate`); then substring match against the delegation stems `agent subagent task workflow cron schedul worktree delegate spawn dispatch handoff remote sendmessage monitor` denies with the matched stem. These lists are ported verbatim from upstream and stay inline constants.
  - `runHook(name string, stdin io.Reader, stdout, stderr io.Writer) int` in cmd/cfo: the single dispatcher every `cfo hook <name>` case routes through; unknown hook names print `cfo hook: unknown hook "<name>"` to stderr and exit 2 is WRONG here: they exit 0 (a future Claude version invoking a newer hook name must not break the tool call) with the message on stderr.
  - `cfo hook pretool-subagent` behavior, in order: read payload (fail open on transport error, exit 0); resolve home; `IsPrimary` false, exit 0 silent; `CFO_ALLOW_SUBAGENT=1`, exit 0 silent; classify ToolName; on deny, `DenyPreTool` with exactly this message:
    `[subagent-dispatch] the CFO primary dispatches through the fleet, not the harness's own delegation tools: work started that way has no durable fleet record and dies with this session. Use the fleet dispatch path once Plan 3 lands it (blocked tool: <tool>, delegation-shaped on "<stem>"). Launch the session with CFO_ALLOW_SUBAGENT=1 for a deliberate exception.`

- [ ] **Step 1: Write the failing classifier tests**

`subagent_test.go`, table-driven over: `Agent` denies on stem agent; `SendMessage` denies on sendmessage; `TaskCreate` allows (plan-only); `TaskUpdate` allows (plan-only); `TaskOutput` allows (observe-only); `TaskStop` allows; `CronList` allows (the observe-only allowlist beats the `cron` stem, which is the only allowlist entry outside the task family that a stem would otherwise shadow); `mcp__herdr__spawn_task` allows (MCP); `Bash` allows; `Read` allows; `CronCreate` denies on cron; `EnterWorktree` denies on worktree; `Workflow` denies on workflow; case-insensitivity: `AGENT` denies.

- [ ] **Step 2: Write the failing hook dispatcher tests**

`hook_test.go` drives `runHook` directly with `bytes.Buffer` streams and a fixture home built by a shared helper:

```go
// newPrimaryHome creates AGENTS.md, state/, and a plain git checkout in a
// temp dir, sets CFO_HOME to it, and returns the dir.
func newPrimaryHome(t *testing.T) string
```

Cases:
1. Deny in primary home: payload `{"session_id":"s","tool_name":"Agent"}`, expect exit 2, empty stdout, stderr parses as the deny envelope and systemMessage contains `delegation-shaped on "agent"`.
2. Inert without state/: same payload, home without `state/`, expect exit 0, both streams empty.
3. Escape hatch: `CFO_ALLOW_SUBAGENT=1` in primary home, expect exit 0 silent.
4. MCP passthrough in primary home: tool `mcp__x__spawn`, exit 0 silent.
5. Transport failure: stdin `garbage`, exit 0 silent.
6. Unknown hook name: `runHook("no-such", ...)` exit 0, stderr mentions unknown hook.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/guard ./cmd/cfo -v`
Expected: FAIL to build.

- [ ] **Step 4: Implement**

`subagent.go` is a pure function over two `map[string]bool` allowlists and a `[]string` stems slice.
`hook.go` defines `runHook` with a `switch name`, one private function per hook; `main.go` gains `case "hook":` requiring `args[1]` (missing name prints usage to stderr, exit 2) and the usage constant gains `  hook <name>  claude code hook entry points (session-start, pretool-arm, pretool-cd, pretool-subagent, turnend-guard, stop-autoarm)`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/guard ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 6: E2E verification**

```powershell
& "C:\Program Files\Go\bin\go.exe" build ./cmd/cfo
$fix = "$env:TEMP\cfo-e2e-sub"; New-Item -ItemType Directory -Force "$fix\state" | Out-Null
Set-Content "$fix\AGENTS.md" "# home"; git -C $fix init 2>$null
$env:CFO_HOME = $fix
'{"session_id":"s","tool_name":"Agent"}' | .\cfo.exe hook pretool-subagent; "exit: $LASTEXITCODE"
```
Expected: stderr shows the deny envelope, `exit: 2`.
Then unset CFO_HOME, run the same pipe from the repo root (no state/ dir): expect silence and exit 0.
Delete `.\cfo.exe`.

- [ ] **Step 7: Full-suite check and commit**

```powershell
git add internal/guard cmd/cfo
git commit -m "feat(hook): pretool-subagent guard and cfo hook dispatcher"
```

---

### Task 7: pretool-arm and pretool-cd guards

**Files:**
- Create: `internal/guard/armcd.go`
- Test: `internal/guard/armcd_test.go`
- Modify: `cmd/cfo/hook.go` (two new cases)
- Modify: `cmd/cfo/hook_test.go` (dispatcher cases)

**Interfaces:**
- Consumes: `claudehook` transport, `home` scope (both guards use `IsPrimary`; the upstream cd-guard's looser predicate is deliberately tightened to IsPrimary so both guards share the inert-in-dev guarantee; document this as a sanctioned deviation).
- Produces:
  - `guard.ClassifyArm(command string) (code, reason string, deny bool)`.
    Fast path: a command with no watcher token allows immediately, whatever else it contains. The watcher token is the regexp `(?i)\b(fm-watch|cfo(\.exe)?\s+watch)\b` matched against the NORMALIZED command (see Step 3: backslashes become forward slashes, quote characters are deleted). ANSI-C quoting markers (`$'` or `$"`) are NOT part of the fast-path test; they only select a deny code once a watcher token is already present, so an ordinary command such as `grep $'\t' file` allows here and never reaches the ladder.
    Deny codes, checked in this order on a watcher-referencing command: `broad-watcher-kill` (contains `pkill`, `taskkill`, or `Stop-Process` in the same command as a watcher token), `watcher-background` (trailing `&` or `Start-Job`/`Start-Process` with a watcher token), `watcher-pipeline` (watcher token inside a `|` segment), `watcher-redirection` (watcher token with `>`, `>>`, or `2>`), `watcher-bundled` (watcher token in a command also containing `&&`, `;`, or `||`), `watcher-nested` (watcher token inside `$(`, backticks, or an `eval`/`bash -c`/`powershell -Command` wrapper), `unclassifiable-protected-command` (ANSI-C markers present with a watcher token), and finally `watcher-direct` for any remaining watcher invocation. The ladder is entered only by a command that already matched the watcher token, so it carries no unconditional final deny: a command without the token allowed at the fast path and never gets here.
    v1 posture: every watcher-family Bash invocation denies; the repair path for a down watcher is fixing the hook registration, never running the watcher from the agent shell. The specific codes exist for diagnostics parity with upstream.
  - `guard.ClassifyCd(command string) (code, reason string, deny bool)`.
    Denies a command in which ANY top-level statement's command word is `cd`, `pushd`, `popd`, or `Set-Location`. Claude Code's Bash tool keeps its working directory between calls, so a relocation anywhere in the command outlives the tool call; upstream iterates every top-level node for exactly this reason and a final-statement-only rule is a regression, not a simplification.
    A top-level statement is one lying outside every balanced `(...)` group: a POSIX subshell relocation such as `(cd sub && make)` is exempt because it dies with the subshell, but `Set-Location` inside parentheses is NOT exempt, because PowerShell parentheses are a grouping expression rather than a subshell and the relocation persists.
    Deny code is always `cwd-relocation`.
  - Reason text per code, contractual alongside the code because it is the half of the deny message the Supreme Overlord actually reads:
    - `broad-watcher-kill`: `a broad process kill in the same command as a watcher invocation takes supervision down along with its intended target`
    - `watcher-background`: `backgrounding the watcher orphans it from the Stop-owned auto-arm that is supposed to host it`
    - `watcher-pipeline`: `piping the watcher's output swallows the wake reason the auto-arm returns`
    - `watcher-redirection`: `redirecting the watcher's output swallows the wake reason the auto-arm returns`
    - `watcher-bundled`: `bundling the watcher with other statements hides which half of the command supervision depends on`
    - `watcher-nested`: `nesting the watcher inside a substitution or an interpreter wrapper hides it from this guard's diagnostics`
    - `unclassifiable-protected-command`: `this command quotes a watcher invocation in a form this guard cannot classify safely`
    - `watcher-direct`: `the watcher is armed by the Stop-owned auto-arm hook, never from the agent shell`
    - `cwd-relocation`: `Claude Code's Bash tool keeps its working directory between calls, so this relocation would outlive the tool call`
  - Hook behavior for both `cfo hook pretool-arm` and `cfo hook pretool-cd`: transport fail-open; `IsPrimary` gate; classify `payload.Command`; deny message is `[<code>] <reason>` through `DenyPreTool`.

- [ ] **Step 1: Write the failing classifier tests**

`armcd_test.go`, table-driven.
Every deny row asserts the reason string from the table above as well as the code, because the reason is the half of `[<code>] <reason>` a human reads and an unasserted string drifts.
Arm cases (command, wantDeny, wantCode):
1. `echo hi`, allow.
2. `cfo watch`, deny, watcher-direct.
3. `cfo watch &`, deny, watcher-background.
4. `cfo watch | tee log`, deny, watcher-pipeline.
5. `cfo watch > out.txt`, deny, watcher-redirection.
6. `cd x && cfo watch`, deny, watcher-bundled.
7. `$(cfo watch)`, deny, watcher-nested.
8. `pkill -f cfo watch`, deny, broad-watcher-kill.
9. `bin/fm-watch.sh`, deny, watcher-direct (legacy token kept during transition).
10. `echo $'cfo watch'`, deny, unclassifiable-protected-command.
11. `cfo watchdog-config`, allow (the token regexp ends in `\b`, so `watchdog` never matches).
12. `git log --oneline`, allow.
13. `.\cfo.exe watch`, deny, watcher-direct (the canonical Windows spelling, and the one the plan's own e2e steps type).
14. `cfo.exe watch &`, deny, watcher-background.
15. `C:\dev\code-goblins\cfo.exe watch`, deny, watcher-direct (this is why normalization maps `\` to `/` instead of deleting it: deletion would glue the path into `code-goblinscfo.exe` and destroy the word boundary).
16. `grep $'\t' file`, allow (an ANSI-C marker with no watcher token is not this guard's business).

Cd cases (command, wantDeny):
1. `cd C:\other`, deny.
2. `pushd ..`, deny.
3. `cd sub && go test ./...`, deny (the relocation is a top-level statement and outlives the tool call).
4. `(cd sub && make)`, allow (the relocation is inside a subshell group).
5. `go test ./... && (cd sub)`, allow (subshell group as the final statement).
6. `go test ./... && cd sub`, deny (bare relocation as a later top-level statement).
7. `go test ./... ; popd`, deny.
8. `Set-Location C:\`, deny.
9. `(Set-Location C:\)`, deny (PowerShell parentheses group, they do not fork).
10. `echo cd`, allow (cd is an argument, not a statement head; tokenize on statement separators `&&`, `;`, `||`, `|` and inspect each segment's first word).
11. `git commit -m "wip; cd later"`, allow (the separator sits inside a quoted span and must not create a phantom statement).
12. `echo "a | popd"`, allow (same rule, pipe form).
13. `git -C C:\x status`, allow.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/guard -v`
Expected: FAIL to build with `undefined: ClassifyArm`.

- [ ] **Step 3: Implement**

`armcd.go`: both classifiers are pure string analysis (`strings`, `regexp` compiled at package init).
Normalization helper shared by both, used for TOKEN detection only, with the original string kept for STRUCTURE detection (separators, parens, redirections): lowercase, replace every `\` with `/` (never delete it, or `C:\dev\repo\cfo.exe watch` collapses into `code-goblinscfo.exe` and loses the word boundary the token regexp needs), and delete single quotes, double quotes and backticks.
Segment splitting for ClassifyCd skips single-quoted, double-quoted and backtick-quoted spans before looking for `&&`, `;`, `||` or `|`, so a separator inside an argument never creates a phantom statement whose head reads as `cd`; a guard that blocks the crew's own commits is worse than one that misses an obfuscated bypass, which is why upstream imports a real lexer and documents that it prioritizes zero false blocks.
Keep each classifier under 80 lines; the deny-code ladder is a sequence of if-checks in the documented order, not a table of regexes per code.

- [ ] **Step 4: Wire the hook cases and dispatcher tests**

Add `hook_test.go` cases: arm deny in primary home (payload command `cfo watch &`, expect exit 2 and code `watcher-background` in systemMessage); arm ALLOW in primary home (payload command `git log --oneline`, expect exit 0 with both stdout and stderr empty, which is the only case that can catch a hook ignoring ClassifyArm's deny bool and denying every Bash call); arm inert without state/; cd deny (`cd C:\`); cd allow (`go test ./...`, expect exit 0 with both streams empty - note `cd sub && go test` is a DENY under the any-top-level-statement rule and can no longer serve as the allow case).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/guard ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 6: E2E verification**

Build, then in the Task 6 fixture home pipe `{"session_id":"s","tool_name":"Bash","tool_input":{"command":"cfo watch &"}}` into `.\cfo.exe hook pretool-arm`: expect deny envelope, exit 2.
Pipe `{"tool_input":{"command":"cd C:\\"}}` into `hook pretool-cd`: expect deny, exit 2.
From the dev repo root without CFO_HOME: both exit 0 silent.
Delete `.\cfo.exe`.

- [ ] **Step 7: Full-suite check and commit**

```powershell
git add internal/guard cmd/cfo
git commit -m "feat(hook): pretool-arm and pretool-cd seatbelt guards"
```

---

### Task 8: internal/watch, the triage loop core

**Files:**
- Create: `internal/watch/watch.go`
- Test: `internal/watch/watch_test.go`
- Modify: `cmd/cfo/main.go` (add `watch` case and usage line)
- Modify: `cmd/cfo/main_test.go` (one `watch` dispatch row)

**Interfaces:**
- Consumes: `wake.Append`, `wake.PublishEpisode`, the Task 5 named-lock family (`lock.AcquireNamedOwner`, `lock.HeldByNamed`, `lock.ReleaseNamed`), `home.Home`, `claudehook.Seconds` for intervals.
- Produces:
  - `type watch.Config struct { Home home.Home; Poll, SignalGrace, Heartbeat, HeartbeatMax time.Duration; Sleep func(time.Duration); WaitEvent func(timeout time.Duration) bool; Cleanup func() }` with `watch.ConfigFromEnv(h home.Home) Config` filling defaults (Sleep defaults to time.Sleep; WaitEvent and Cleanup default to nil, meaning pure timer mode with nothing to release until Task 9 supplies both). ConfigFromEnv clamps every interval it reads to at least 1s: `claudehook.Seconds` shipped in Task 2 with no clamp, so `CFO_POLL=0` would spin the loop rewriting signatures and touching the beat, and `CFO_POLL=-5` would convert to roughly 49 days of wait so the watcher never beats and WatcherHealthy reads stale against a live process. The clamp belongs at this consumer; Task 2 is shipped and is not reopened.
  - `watch.Run(cfg Config) (reason string, err error)`: acquires the singleton, then defers `lock.ReleaseNamed(cfg.Home.State, ".watch.lock")` FIRST and `cfg.Cleanup()` (when non-nil) SECOND, so Go's LIFO ordering runs Cleanup before the release. The singleton comes from `lock.AcquireNamedOwner(cfg.Home.State, ".watch.lock", os.Getpid(), "watch")`. Then it loops: touch `state/.last-watcher-beat`; scan signals; on changed status files, sleep SignalGrace, rescan, append one wake record PER CHANGED FILE (`kind signal`, `key <that file's own basename>`, `detail "signal:<space-joined relative paths>"`, the same detail on every record of the cycle), call `CommitSignatures` for exactly those changes once every Append has returned nil, and return the shared detail as the reason; when the heartbeat is due, append `(heartbeat, heartbeat, "heartbeat")` and return `"heartbeat"`.
    One record per changed file is upstream's shape (`bin/fm-watch.sh:977`) and it is what makes Deduped's `(kind,key)` fold correct: a single record keyed on an arbitrary member of the changed set lets a later cycle overwrite an earlier cycle's mention of a file whose signature has already advanced, so that file's completion is never presented and is then acked away.
    Between checks it waits via `WaitEvent(Poll)` when non-nil else `Sleep(Poll)`.
    On any return path except lock-lost, `PublishEpisode(cfg.Home.State)` is called BEFORE releasing the singleton (upstream's watcher-down marker); when the singleton was lost to a successor (`lock.HeldByNamed(cfg.Home.State, ".watch.lock", os.Getpid())` no longer names us), return `("", nil)` without publishing.
  - Heartbeat cadence persists across watcher exits, because Run returns on every heartbeat close and Run-local state could never carry it: `state/.last-heartbeat` is an empty mtime beacon recording when the last heartbeat closed, and `state/.heartbeat-streak` is a single integer line recording how many consecutive quiet heartbeats have closed, read clamped to 0..8 so the shift cannot overflow. At Run start the due interval is `min(Heartbeat << streak, HeartbeatMax)`, and the heartbeat is due when the age of `.last-heartbeat` reaches it; a missing `.last-heartbeat` is created at Run start and the heartbeat becomes due one interval later, never immediately, so a fresh home does not fire a heartbeat wake at t=0. A heartbeat close touches `.last-heartbeat` and increments `.heartbeat-streak`; a signal close touches `.last-heartbeat` and REMOVES `.heartbeat-streak`.
  - Both `state/.last-watcher-beat` and `state/.last-heartbeat` are stamped and compared with the WALL clock (`time.Now`), because `supervise.WatcherHealthy` reads the beat from another package against the real clock and a beat stamped from an injected clock would make a healthy watcher read as stale. That is why `Config` carries no injectable clock at all: every timing decision in the loop reads a beacon's mtime, tests move time with `os.Chtimes` on those beacons, and `Sleep` is the loop's only timing seam.
  - `watch.ScanSignals(stateDir string) ([]Change, error)` with `type Change struct { Name, Sig string }`: a PURE READ that compares each `*.status` and `*.turn-ended` file in stateDir against the persisted `size:mtime` signature in `state/.seen-<sanitized>` and returns the entries whose signature moved, writing nothing anywhere.
  - `watch.CommitSignatures(stateDir string, changes []Change) error`: writes those signatures, and Run calls it only after every wake Append for the cycle has returned nil. Detection and commitment are separate so a watcher killed inside the SignalGrace window, or any error return before Append, re-reports the same signal on the next start instead of swallowing it permanently. Signatures still persist across watcher restarts (that is the point: signals landing while no watcher runs are caught on the next start), and a crash between Append and CommitSignatures costs a duplicate wake, which Deduped folds away, rather than a lost one. Because ScanSignals commits nothing, the post-grace rescan already returns the union of both scans and no separate coalescing step is needed.
  - `watch.Sanitize(name string) string`: maps every character outside `[A-Za-z0-9_-]` to `_` (covers `:` `/` `\` `.` which are illegal or ambiguous in NTFS filenames). The mapping is deliberately lossy and collision-prone in principle, which is safe here because a collision only merges two signatures and the worst consequence is a duplicate wake that Deduped folds away; never widen it to preserve dots, or `.seen-a.status` becomes indistinguishable from a hidden state file.
  - `cfo watch` subcommand: resolves home; refuses (exit 1, message) when not IsPrimary; otherwise calls Run with env config and prints the reason line to stdout and exits 0; a non-nil error prints it to stderr and exits 1; an empty reason with a nil error (the singleton lost to a successor) prints nothing and exits 0. Intended for manual diagnostics only; the hooks are the production entry.

- [ ] **Step 1: Write the failing tests**

`watch_test.go` (all with `t.TempDir()` state dirs and tiny injected intervals; heartbeat timing is driven with `os.Chtimes` on the beacons rather than a fake clock, because the beacons carry wall-clock time):
1. `TestSanitize`: `g1.status` becomes `g1_status`; `w:1/2` becomes `w_1_2`.
2. `TestScanSignalsDetectsNewAndChanged`: first scan of a dir with `a.status` reports `a.status` (new file counts as changed); CommitSignatures for that change; second scan reports nothing; append a line to the file, third scan reports it again.
3. `TestScanSignalsCommitIsTheOnlyCommitment`: the first scan reports `a.status` and `state\.seen-a_status` does NOT yet exist; a second scan without CommitSignatures reports it again; after CommitSignatures the signature file exists and the next scan is quiet; delete `state\.seen-a_status` and the scan reports it again, proving the on-disk signature is the sole source of truth.
4. `TestRunClosesOnSignal`: fixture with TWO status files changed in the same cycle; a fake Sleep that appends to one of them on its first invocation; Run returns a reason starting `signal:` naming both files, TWO wake records exist with kind signal and distinct keys carrying the identical detail, both `.seen-*` signature files now exist, and `.watcher-down` reads `pending:1`.
5. `TestRunClosesOnHeartbeat`: no status changes, `state\.last-heartbeat` pre-created and `os.Chtimes`'d one Heartbeat into the past with no streak file; Run returns `heartbeat`, one heartbeat wake record exists, and `state\.heartbeat-streak` now reads 1.
6. `TestHeartbeatBackoffDoublesAndResets`: with `state\.heartbeat-streak` pre-written as 1 and `.last-heartbeat` aged by exactly Heartbeat, the first pass must NOT close on heartbeat, because the due interval is twice Heartbeat. Run has no iteration cap and returns only on a close, so "does not close" cannot be asserted directly and needs the Sleep seam: inject a Sleep that counts its calls and, on its FIRST call, `os.Chtimes`-ages `.last-heartbeat` to twice Heartbeat. Assert Run returns `heartbeat` only after at least one Sleep call, which proves the pass before that call did not fire at one Heartbeat, and that `state\.heartbeat-streak` now reads 2. A signal close then removes the streak file.
7. `TestRunSingletonExcludes`: hold `state/.watch.lock` via AcquireNamedOwner for a live foreign process (the ping-child pattern from the lock tests); Run returns an error wrapping lock.ErrHeld.
8. `TestRunReturnsQuietlyWhenSingletonStolen`: Run starts holding the lock, with an injected Sleep whose first call overwrites `state\.watch.lock` with a record naming a live foreign pid; Run returns `("", nil)`, no wake record is appended, and `state\.watcher-down` is never created. Without this case the mid-loop ownership re-check can be omitted entirely, and a displaced watcher then publishes a second downtime episode that churns the recovery generation AckEpisode keys on.
9. `TestRunTouchesBeat`: after a heartbeat close, `state/.last-watcher-beat` exists and `time.Since(fi.ModTime())` is under 30s, asserted against the wall clock.
10. `main_test.go` dispatch row: `{name: "watch refuses outside a primary home", args: []string{"watch"}, wantExit: 1, wantStderr: "not a primary", env: map[string]string{"CFO_HOME": t.TempDir()}}`, using the optional `env` field Task 5 added to the table struct.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/watch ./cmd/cfo -v`
Expected: FAIL to build.

- [ ] **Step 3: Implement**

`lock.go`: no change. Task 5 already shipped the named-lock family.
`watch.go` implementation constraints:
- The loop is single-goroutine; no channels except an optional WaitEvent hook.
- Nothing about the heartbeat cadence is Run-local: it lives in `state/.last-heartbeat` and `state/.heartbeat-streak` exactly as the Interfaces specify, because Run returns on every close.
- Beat touch uses `os.Chtimes` when the file exists, else an empty `os.WriteFile` (no fsx needed for an mtime beacon), always with `time.Now()`.
- Register the two defers in the order the Interfaces bullet gives: `lock.ReleaseNamed` first, `cfg.Cleanup()` (when non-nil) second, so LIFO runs Cleanup on every return path before the singleton is released.
- One actionable reason closes one cycle, and that cycle's records are appended before the singleton releases: a signal close appends one record per changed file, all carrying the same detail, and a heartbeat close appends exactly one; comment: "one actionable reason closes one watcher cycle; continuity is the arm layer's job".
- NOT PORTED IN V1 comments at the stale-scan and check-sweep insertion points naming Plan 3 and Plan 4.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/watch ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 5: E2E verification**

```powershell
& "C:\Program Files\Go\bin\go.exe" build ./cmd/cfo
$fix = "$env:TEMP\cfo-e2e-watch"; Remove-Item -Recurse -Force $fix -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force "$fix\state" | Out-Null
Set-Content "$fix\AGENTS.md" "# home"; git -C $fix init 2>$null
$env:CFO_HOME = $fix; $env:CFO_POLL = "1"; $env:CFO_SIGNAL_GRACE = "1"
$p = Start-Process -FilePath .\cfo.exe -ArgumentList "watch" -PassThru -NoNewWindow -RedirectStandardOutput "$fix\watch-out.txt"
$deadline = (Get-Date).AddSeconds(15)
while (-not (Test-Path "$fix\state\.last-watcher-beat")) { if ((Get-Date) -gt $deadline) { throw "watcher never beat" }; Start-Sleep -Milliseconds 100 }
Start-Sleep -Milliseconds 500
Set-Content "$fix\state\g1.status" "working"
Wait-Process -Id $p.Id -Timeout 30 -ErrorAction SilentlyContinue
Get-Content "$fix\watch-out.txt"; .\cfo.exe drain
```
Expected: the watch process exits on its own within the timeout, `watch-out.txt` contains a `signal:` line naming `g1.status`, and `cfo drain` shows one pending signal record plus a pending recovery episode with the WAKE_ACK_REQUIRED line.
The status file is created only AFTER the first beat proves the watcher is looping: a file present before the watcher starts is reported by the very first scan (a first sighting counts as a change, which is the cross-restart contract), so the cycle would close before the write this step is meant to be triggered by, and the proof would be vacuous.
The beat wait is deadline-bounded because the beacon never appears at all when the watcher refuses (git init failed, home not primary), and an unbounded `while` would hang the step instead of failing it. The extra 500ms after the beat exists because the beat is touched at the TOP of the loop, before ScanSignals and before the wait is armed, so a status file written into that window is only noticed after a full Poll interval.
Clean up env vars, `.\cfo.exe`, and `Remove-Item -Recurse -Force $fix`.

- [ ] **Step 6: Full-suite check and commit**

```powershell
git add internal/watch cmd/cfo
git commit -m "feat(watch): triage loop with signal scan, heartbeat, singleton"
```

---

### Task 9: filesystem notifications with a permanent-degrade circuit breaker

**Files:**
- Create: `internal/watch/notify_windows.go`
- Test: `internal/watch/notify_windows_test.go`
- Modify: `internal/watch/watch.go` (ConfigFromEnv wires WaitEvent and Cleanup)

**Interfaces:**
- Consumes: stdlib `syscall` only.
- Produces:
  - `watch.NewDirWaiter(dir string) (*DirWaiter, error)`: opens the directory with `syscall.CreateFile(..., FILE_LIST_DIRECTORY, FILE_SHARE_READ|WRITE|DELETE, nil, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OVERLAPPED, 0)` and an event handle for overlapped completion.
  - `(*DirWaiter).Wait(timeout time.Duration) bool`: issues `syscall.ReadDirectoryChanges` for `FILE_NOTIFY_CHANGE_FILE_NAME|LAST_WRITE|SIZE`, waits with `syscall.WaitForSingleObject(event, ms)`, returns true on a change event and false on timeout.
    Any API failure counts one breaker strike; after `EventCapFailMax` (default 3) CONSECUTIVE failures the waiter permanently degrades for its lifetime: Wait becomes `time.Sleep(timeout); return false`. A success resets the strike counter to zero, and the counter is a consecutive run, never a lifetime total: a monotonic counter would permanently drop a home to 15s polling after three scattered failures under intermittent AV interference, which is the exact scenario this breaker exists to survive. Degradation is one-way by design (upstream's fail-closed-to-slow-but-correct posture; Windows directory watches silently die under AV filter drivers).
  - `(*DirWaiter).Degraded() bool` for tests and logs; `(*DirWaiter).Close()`.
  - `watch.ConfigFromEnv(h home.Home) Config` now attempts `NewDirWaiter(h.State)`; on success it wires BOTH `cfg.WaitEvent = waiter.Wait` and `cfg.Cleanup = waiter.Close`; on failure it leaves both nil (pure timer mode with nothing to release). Wiring Wait without Cleanup is the defect this bullet exists to prevent: `watch.Run` defers `cfg.Cleanup()` on every return path, and without it the kernel keeps a pointer into a Go heap buffer owned by a waiter that becomes unreachable the moment Run returns. Either way ConfigFromEnv fills every interval from the env table, clamped to at least 1s.

- [ ] **Step 1: Write the failing tests**

`notify_windows_test.go`:
1. `TestWaitSeesFileWrite`: waiter on a temp dir; goroutine writes a file after 100ms; `Wait(5*time.Second)` returns true in well under 3s (assert elapsed).
2. `TestWaitTimesOutQuietly`: `Wait(200*time.Millisecond)` on an untouched dir returns false, elapsed at least 150ms.
3. `TestBreakerDegradesAfterThreeFailures`: `NewDirWaiter` on a valid dir, then `Close()` the handles, then call `Wait(10*time.Millisecond)` four times; after the third failure `Degraded()` is true and the fourth call returns false after sleeping the timeout without touching the API.
4. `TestConfigFromEnvWiresWaiter`: ConfigFromEnv on a home with an existing state dir yields non-nil WaitEvent; on a home whose state dir does not exist yields nil WaitEvent.
5. `TestWaitTimeoutsLeaveNothingOutstanding`: call `Wait(10*time.Millisecond)` fifty times on an untouched dir; every call returns false, `Degraded()` is still false, and the waiter closes cleanly afterwards. A timeout path that strands its I/O accumulates one pending request per quiet cycle over an eight-hour watch, and this is the case that catches it.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/watch -v`
Expected: FAIL to build with `undefined: NewDirWaiter`.

- [ ] **Step 3: Implement**

Implementation notes for `notify_windows.go` (write real code preserving these decisions):
- One reusable 4KB buffer per waiter; the notification CONTENT is discarded, only the wake-up matters (the scan logic re-derives changes from signatures, so lost or coalesced events are harmless).
- The stdlib `syscall` package exports no event APIs, and `golang.org/x/sys` stays banned, so the overlapped event is created once and reset before each issue through `syscall.NewLazyDLL("kernel32.dll").NewProc("CreateEventW")` and `NewProc("ResetEvent")` driven by `proc.Call`. Any kernel32 entry point absent from `syscall`'s exported surface goes through NewLazyDLL the same way; check the exported surface before assuming a name exists.
- At most one ReadDirectoryChangesW may be in flight per waiter, because one OVERLAPPED and one buffer are reused for the waiter's whole life. On WAIT_TIMEOUT, call `syscall.CancelIoEx(dirHandle, &ov)` and drain the completion by waiting on the event once more before returning false; never issue a second read against an OVERLAPPED whose previous I/O has not completed.
- `Close` cancels in-flight IO with `syscall.CancelIoEx(dirHandle, &ov)` (`CancelIo` is thread-scoped and Go goroutines migrate threads, so it would be a no-op here), then closes both handles, sets them to `syscall.InvalidHandle`, and marks the waiter closed. Wait on a closed waiter counts a failure strike and returns false immediately without touching the API, so the EventCapFailMax breaker stays the only thing that sets `Degraded()`. The degraded check precedes the closed check, so a degraded waiter still sleeps the timeout on every later call, including after Close.
- The strike rule is narrow on purpose: a wait aborted because `Close()` canceled the I/O is a failure strike, while the self-cancel that completes an ordinary WAIT_TIMEOUT is the normal quiet path and scores no strike. Charging the quiet path would trip the breaker after three idle cycles and drop every home to timer polling within a minute.
- Keep every raw handle private; the exported surface is exactly NewDirWaiter, Wait, Degraded, Close.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/watch -v`
Expected: PASS.

- [ ] **Step 5: E2E verification**

Repeat the Task 8 e2e run with a FRESH fixture containing `state\` and NO status file, and WITHOUT setting `CFO_POLL` (default 15s), with `CFO_SIGNAL_GRACE=1`:

```powershell
& "C:\Program Files\Go\bin\go.exe" build ./cmd/cfo
$fix = "$env:TEMP\cfo-e2e-notify"; Remove-Item -Recurse -Force $fix -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force "$fix\state" | Out-Null
Set-Content "$fix\AGENTS.md" "# home"; git -C $fix init 2>$null
$env:CFO_HOME = $fix; Remove-Item Env:\CFO_POLL -ErrorAction SilentlyContinue; $env:CFO_SIGNAL_GRACE = "1"
$p = Start-Process -FilePath .\cfo.exe -ArgumentList "watch" -PassThru -NoNewWindow -RedirectStandardOutput "$fix\watch-out.txt"
$deadline = (Get-Date).AddSeconds(15)
while (-not (Test-Path "$fix\state\.last-watcher-beat")) { if ((Get-Date) -gt $deadline) { throw "watcher never beat" }; Start-Sleep -Milliseconds 100 }
Start-Sleep -Milliseconds 500
$sw = [Diagnostics.Stopwatch]::StartNew()
Set-Content "$fix\state\g1.status" "working"
Wait-Process -Id $p.Id -Timeout 30 -ErrorAction SilentlyContinue
$sw.Stop(); "elapsed ms: $($sw.ElapsedMilliseconds)"
```
Expected: the close arrives in under 5s. That number is the proof by itself and needs no second run: in pure timer mode the loop is parked in `Sleep(15s)` when the file appears, so no close is possible before 16s, and only the DirWaiter can explain an early wake.
The beat wait carries the same deadline and the same 500ms settle as Task 8's, and here the settle is load-bearing rather than merely tidy: the beat is touched before the waiter is armed, so a write landing in that window is caught only by the next 15s poll and would fail the under-5s assertion intermittently.
Clean up env vars, `.\cfo.exe`, and `Remove-Item -Recurse -Force $fix`.

- [ ] **Step 6: Full-suite check and commit**

```powershell
git add internal/watch
git commit -m "feat(watch): directory-change waits with permanent-degrade breaker"
```

---

### Task 10: internal/supervise and cfo hook turnend-guard

**Files:**
- Create: `internal/supervise/supervise.go`
- Test: `internal/supervise/supervise_test.go`
- Modify: `cmd/cfo/hook.go` (turnend-guard case)
- Modify: `cmd/cfo/hook_test.go` (guard cases)

**Interfaces:**
- Consumes: the Task 5 named-lock family (`lock.ReadNamed`, `lock.AcquireNamedOwner`, `lock.ReleaseNamed`), `lock.Read`/`lock.HeldBy`, `home.Home`, `claudehook` env and emitters, `fsx`.
- Produces (all take stateDir first):
  - `supervise.Needed(stateDir string) (bool, string, error)`: true when any `*.meta` exists; the string is `"<N> task(s) in flight"`. The error is the third value rather than a swallowed false, because the failure posture below prescribes a different outcome for an unlistable state directory than for an empty one and a two-value signature makes the two indistinguishable at the call site. Needed lists with `os.ReadDir` and returns its error; `filepath.Glob` is not acceptable here because it swallows the directory read error the third return value exists to expose. NOT PORTED IN V1: procevent sources and x-watch checks.
  - `supervise.WatcherHealthy(stateDir string, grace time.Duration) bool`: the `.watch.lock` record names a live owner (`lock.ReadNamed`, shipped in Task 5, plus `Alive`) AND `.last-watcher-beat` mtime is younger than grace.
  - `supervise.AutoarmOwnsRecovery(stateDir string, grace, epochFresh time.Duration) bool`: WatcherHealthy, OR `.claude-autoarm.lock` names a live holder whose Session field is `autoarm` AND `NotifiedOnce(stateDir)` is false, OR the epoch ledger's outcome is exactly `rewake` with `updated_at` younger than epochFresh AND its `owner_pid` naming a live process.
    Two narrowings, both load-bearing. `failed` and `failed-suppressed` are failure evidence and must never count as proof: they fall through to ChargeBudget so the ladder can escalate (upstream allowed a one-shot `failed` purely to seed its block budget, which the session-scoped counter here does not need). And a running auto-arm stops being proof once the episode has already notified a failure, because otherwise the lock proof is true at every subsequent Stop, the budget is never charged, MarkAlarm never fires, and the auto-arm's own exit 2 loops forever with an empty banner and no way out. After the alarm has fired, later Stops keep blocking with the blind-turn banner: the alarm message is one-time, the block is not. The single attended fail-open is the Stop on which the alarm fires, which is why Task 11's step-7 `AlarmFired` arm yields with exit 0 instead of re-blocking that same Stop from the other side.
    The `owner_pid` liveness requirement is the epoch proof's only consumer of that field, and it is what makes the proof self-invalidating: a `rewake` stamped by an arming hook that has since died is a claim nobody is behind any more, and without the check it would keep the guard open for the whole epochFresh window after its author was gone.
  - Budget over `state/.turnend-claude-blocks` (two lines, `session=` and `count=`; upstream's third `epoch=` line is dropped with the same-epoch dedup it existed for, and the drop is recorded at the NOT PORTED IN V1 comment so the divergence from upstream's file shape reads as deliberate), serialized by `lock.AcquireNamedOwner(stateDir, ".turnend-claude-blocks.lock", os.Getpid(), "budget")` with a deferred `lock.ReleaseNamed`, retried on `lock.ErrHeld` up to 10 times at 50ms (a FILE carrying an owner record, not a directory and not an anonymous sentinel, so a process killed mid-charge is stolen by the next caller instead of wedging the home permanently): `supervise.ChargeBudget(stateDir, session string) (count int, err error)` increments (resetting to 1 when the recorded session differs) and `supervise.ResetBudget(stateDir string) error` removes the budget record plus both failure markers as one group under that same named lock. Upstream's same-epoch dedup within one Stop event is NOT PORTED IN V1 (Claude fires the guard once per Stop); the session-scoped counter is the load-bearing part.
  - Epoch ledger `state/.claude-autoarm-epoch`, single line `epoch=<n> owner_pid=<pid> outcome=<o> updated_at=<unix>`: `supervise.NextEpoch(stateDir string) (int, error)` (increments, writes outcome `arming`), `supervise.SetOutcome(stateDir string, epoch int, outcome string) error`, `supervise.ReadEpoch(stateDir string) (Epoch, error)` with `type Epoch struct { N int; OwnerPID int; Outcome string; UpdatedAt time.Time }`.
  - Failure markers: `supervise.MarkNotified/NotifiedOnce`, `supervise.MarkAlarm/AlarmFired` over `state/.claude-autoarm-failure-notified` and `-alarmed` (empty O_EXCL files; Mark is idempotent).
  - SyncWaitMS is read as `time.Duration(claudehook.Int("CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS", 800, 0, 60000)) * time.Millisecond`; 0 means do not wait at all, and step 4 then performs exactly one check.
- `cfo hook turnend-guard` decision sequence (Claude-only; `stop_hook_active` is read but deliberately ignored, with the upstream 2026-07-21 incident comment):
  1. Transport fail-open; IsPrimary gate (exit 0 silent).
  2. `needed, inFlight, err := Needed(state)`. A non-nil `err` is fail-open: exit 0 silent, touching no state at all, so the budget record and both markers survive an unlistable state directory. `needed` false: `ResetBudget` unless `NotifiedOnce` (a pending failure episode survives quiet turns); exit 0. `inFlight` is the string the step-6 banner renders.
  3. `WatcherHealthy(GuardGrace)`: `ResetBudget`; exit 0.
  4. Poll `AutoarmOwnsRecovery` every 100ms up to SyncWaitMS; proof found: exit 0.
  5. `ChargeBudget`; when count > BlockBudget AND `NotifiedOnce` AND NOT `AlarmFired`: `MarkAlarm`, then `InfoAllow` on stdout with exactly `CFO SUPERVISION IS GENUINELY DOWN: the Stop-owned auto-arm could not restore the watcher and the block budget is exhausted. This turn may end, but supervision stays off. Run cfo doctor, verify the stop-autoarm hook registration in .claude/settings.json, and re-launch the session.` and exit 0.
  6. Otherwise `BlockStop` with this banner (fill N and the beat age; `never` when no beat file):

```
●━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
●  TURN WOULD END BLIND - SUPERVISION IS OFF
●  <N> task(s) in flight, but no live watcher holds this home (last beat: <age|never>).
●  The Stop-owned auto-arm did not claim recovery within the sync window.
●  Repair: verify .claude/settings.json wires "cfo hook stop-autoarm" with asyncRewake, then end the turn again. Run cfo drain for pending wakes.
●━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

Failure posture for supervise state, one rule applied at every step above. A read error from `Needed`, which its third return value makes visible to the caller, is fail-open: exit 0 silent, the same posture as a transport failure, because a state directory that cannot be listed cannot prove work is in flight. A read error from `WatcherHealthy`, `AutoarmOwnsRecovery`, `NotifiedOnce` or `AlarmFired` yields false, because an unprovable health claim is not proof. A `ChargeBudget` or `ResetBudget` error means the ladder cannot run at all, so the guard skips straight to step 5's attended fail-open: best-effort `MarkAlarm`, the GENUINELY DOWN message on stdout through `InfoAllow`, exit 0. A home whose budget file cannot be written therefore escalates once, tells the operator, and lets the turn end, instead of blocking every Stop forever on a counter that can never advance. This branch is deliberately EXEMPT from the "alarm message is one-time" rule stated on the AutoarmOwnsRecovery bullet: that rule governs the ladder's own escalation, while this one fires whenever the budget file is unwritable and can therefore repeat on every Stop until the home is repaired. Both readings fail open, so the exemption costs nothing; it is written down only so the two rules are not read as contradicting.

- [ ] **Step 1: Write the failing supervise tests**

Table-driven over temp state dirs:
1. Needed false with a nil error on an empty dir, true with a nil error and message `1 task(s) in flight` with `g1.meta` present, and a NON-NIL error when the state directory cannot be listed (pass a path that names a regular file rather than a directory); the paired dispatcher row is guard case 2c below, which pins that the guard exits 0 without removing the budget file.
2. WatcherHealthy false with no lock; false with a live lock but a stale beat (`os.Chtimes` to 10 minutes ago, grace 300s); false with a `.watch.lock` naming an EXITED pid and a beat touched to now, which is the only row the liveness check decides. Build that dead-owner record from outside package lock, where the unexported `writeInfo` of `TestAcquireOwnerStealsFromDeadOwner` is unreachable: run `cmd /c exit 0`, then marshal a `lock.Info` (every field is exported) with `encoding/json` and `os.WriteFile` it to `state\.watch.lock`, carrying `PID` and `OwnerPID` of `cmd.ProcessState.Pid()`, `Start` an hour in the past, and the local hostname. Continue the row list: false with a live named lock and no beat file at all; true with a live named lock (AcquireNamedOwner in-test) and a fresh beat. Both conditions are required and neither substitutes for the other: the shipped `Alive()` fails closed on an unverifiable process, so a lock record alone can read as live indefinitely and the beat is the real liveness evidence.
3. ChargeBudget counts 1, 2, 3 for one session and resets to 1 on session change; then MarkNotified and MarkAlarm, assert NotifiedOnce and AlarmFired are both true, then ResetBudget, then assert the budget file is gone and NotifiedOnce and AlarmFired are both false.
4. NextEpoch returns 1 then 2 across calls; after `SetOutcome(dir, 2, "rewake")`, ReadEpoch returns `N == 2`, `Outcome == "rewake"`, `OwnerPID == os.Getpid()`, and `UpdatedAt` within 2s of `time.Now()`. Do not assert strict monotonicity of the timestamp: `updated_at` is unix seconds and two calls in the same second write the same integer.
5. AutoarmOwnsRecovery true via each of the three proofs independently (a healthy watcher; a live `.claude-autoarm.lock` with Session `autoarm` and no notified marker; a `rewake` epoch stamped now through `NextEpoch` plus `SetOutcome`, so its `owner_pid` is this live test process), plus six negative rows, because a single all-absent negative cannot discriminate any of the predicates and each one fails the guard OPEN: (a) no proofs at all; (b) a live `.claude-autoarm.lock` whose Session is `""`; (c) a live `.claude-autoarm.lock` with Session `autoarm` while `MarkNotified` has been called; (d) epoch outcome `rewake` with `updated_at` 60s in the past against epochFresh 15s; (e) epoch outcome `failed` with a fresh timestamp, and the same for `failed-suppressed` and `arming`; (f) a FRESH `rewake` whose `owner_pid` is the pid of an exited `cmd /c exit 0` child, hand-written as an epoch line rather than through NextEpoch. All six false. Row (d) is the one that matters most: an implementation reading the outcome without the freshness window makes one historical `rewake` satisfy the guard forever, so turnend-guard never blocks a blind turn again for the life of that home. Row (f) is what gives `owner_pid` a consumer: it fails only for an implementation that checks the recorded pid is still alive.
6. Markers: NotifiedOnce false, MarkNotified, true; Mark is idempotent.
7. ChargeBudget succeeds when `state\.turnend-claude-blocks.lock` already exists holding a record for a dead pid, written the same way row 2 writes its dead `.watch.lock` record (marshal a `lock.Info` with `encoding/json`, `Start` an hour in the past): the dead holder is stolen and the count still advances.

- [ ] **Step 2: Write the failing guard dispatcher tests**

`hook_test.go` cases driving `runHook("turnend-guard", ...)` with `CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS=500` unless a case says otherwise:
1. Not primary: exit 0 silent.
2a. Primary, no metas, budget pre-written with `count=2`, no notified marker: exit 0 and the budget file is gone.
2b. Primary, no metas, budget pre-written with `count=2` and `MarkNotified` already called: exit 0, the budget file still reads `count=2`, and the notified marker still exists. This is the only test of the `unless NotifiedOnce` clause, and without it an unconditional reset ships green.
2c. Primary home whose `state\` directory has been made UNLISTABLE, budget pre-written with `count=2`: exit 0 with both streams empty and the budget file left exactly as written. Arrange it with `exec.Command("icacls", state, "/deny", os.Getenv("USERNAME")+":(RD)")`. A non-zero exit from icacls is `t.Fatal`, never a skip, because a silently failing fixture would let the case pass without ever exercising the error path; `t.Skip` fires only when icacls SUCCEEDED and a follow-up `os.ReadDir` still lists the directory, which is what happens in an elevated session. Register the ACL restore with `t.Cleanup` AFTER the `t.TempDir()` call - `exec.Command("icacls", state, "/remove:d", os.Getenv("USERNAME")).Run()` - so LIFO cleanup ordering puts it before TempDir's RemoveAll; without it RemoveAll cannot list `state\` and `t.TempDir` fails the test and leaves the fixture behind. This is the only case that separates the `Needed` error from the `Needed` false path, and it is reachable precisely because `os.Stat` still reports the directory (so the IsPrimary gate passes) while the listing fails: a two-value `Needed` reports false here, calls `ResetBudget`, hits ResetBudget's own error posture, and prints the GENUINELY DOWN message on stdout instead of staying silent.
3. Primary, meta present, healthy watcher (fixture named lock + fresh beat): exit 0.
4. Primary, meta present, nothing healthy: exit 2, stderr contains `TURN WOULD END BLIND` and `1 task(s) in flight`.
5. Primary home with `g1.meta` present, no `.watch.lock`, no beat file, and a fresh epoch outcome `rewake` written moments before through `NextEpoch` plus `SetOutcome`, so the ledger's `owner_pid` names the live test process: exit 0. Without the meta this case exits at step 2 and proves nothing about the proof path.
6. Budget exhaustion path, same fixture (meta present, no `.watch.lock`, no beat file): pre-write the budget with `count=3` and a `session=` equal to the payload's session_id, create the notified marker, expect exit 0 with stdout JSON containing `GENUINELY DOWN`, the alarmed marker now present, and the budget file still present reading `count=4`; a SECOND invocation blocks again with exit 2 (the alarm is one-time).
7. Beat-age rendering: primary home, `g1.meta` present, `state\.last-watcher-beat` present but `os.Chtimes`'d to 10 minutes ago, no `.watch.lock`; exit 2, and stderr contains `last beat:` followed by an age token rather than the word `never`.
8. Polling actually polls: primary home, `g1.meta` present, no `.watch.lock`, no beat file, and a goroutine that writes a fresh `rewake` epoch 150ms after runHook is invoked; expect exit 0. This is the only case that distinguishes a poll from a single check.
9. Zero window: the same fixture with `CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS=0` and the same 150ms goroutine; expect exit 2, proving the guard does not wait when the window is zero.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/supervise ./cmd/cfo -v`
Expected: FAIL to build.

- [ ] **Step 4: Implement**

`supervise.go` keeps every function under 40 lines; `withBudgetLock(stateDir string, fn func() error) error` holds `state/.turnend-claude-blocks.lock` as a `lock.Info` record through the Task 5 named-lock family and is shared by ChargeBudget and ResetBudget, so a dead holder is stolen rather than wedging the home.
`lock.ReadNamed` already exists; Task 5 shipped it with the rest of the named-lock family.
The guard case in hook.go is a straight transcription of the six-step sequence; each step carries a one-line comment naming its upstream analogue.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/supervise ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 6: E2E verification**

Build; in a fixture primary home with `state\g1.meta` present and no watcher, pipe `{"session_id":"s1"}` into `.\cfo.exe hook turnend-guard`: expect the blind-turn banner on stderr, exit 2.
Building a live `.watch.lock` from PowerShell is impractical, so prove the other exit-0 arm instead: delete `state\g1.meta` and re-run the same pipe, expecting exit 0 with both streams empty and no budget file left behind. Then `Remove-Item -Recurse -Force $fix` so the next task's fixture starts clean.
Delete `.\cfo.exe`.

- [ ] **Step 7: Full-suite check and commit**

```powershell
git add internal/supervise cmd/cfo
git commit -m "feat(hook): turnend guard with block budget and attended fail-open"
```

---

### Task 11: cfo hook stop-autoarm

**Files:**
- Modify: `cmd/cfo/hook.go` (stop-autoarm case plus its helpers)
- Modify: `cmd/cfo/hook_test.go`
- `internal/lock`: no change; `ReleaseNamed` ships in Task 5.

**Interfaces:**
- Consumes: `proc.FindAncestor`, `lock.AcquireOwner`/`HeldBy`, the Task 5 named-lock family (`lock.AcquireNamedOwner`, `lock.ReleaseNamed`), `supervise.*`, `watch.Run`/`ConfigFromEnv`, `claudehook` transport.
- Produces: the `cfo hook stop-autoarm` behavior, the sole owner of watcher continuity. Claude fires it on every Stop with `asyncRewake: true` and an 8h timeout, undeduplicated; this process IS the watcher host: it runs `watch.Run` in-process and its eventual exit 2 stderr is what rewakes the idle agent.

Decision sequence:
1. Read stdin with `claudehook.ReadPayload`, which drains it so Claude never blocks writing; a transport failure is fail-open, exit 0 silent. Only `session_id` is used; every other decision is state-based. IsPrimary gate, exit 0 silent.
2. Identity gate: `proc.FindAncestor(os.Getpid(), 16, "claude", "node")`; no harness ancestor found: exit 0 (comment: a manual shell invocation must never arm). `CFO_TEST_ANCESTOR_PID`, when set, replaces the walk entirely, and a value the process snapshot no longer resolves fails the gate outright. Session custody: if `lock.HeldBy(state, ancestor.PID)` is false, try `lock.AcquireOwner(state, ancestor.PID, payload.SessionID)`; exit 0 inert on `lock.ErrHeld` (a different live owner holds the home) and equally on `lock.ErrOwnerDead`, which Task 4 shipped and which fires when the harness exits between the ancestry walk and the acquire.
3. Need gate: `needed, _, err := supervise.Needed(state)`; a non-nil `err` is fail-open, exit 0 silent, and `needed` false is exit 0 as well, so an unlistable state directory never arms.
4. Single-flight: `lock.AcquireNamedOwner(state, ".claude-autoarm.lock", os.Getpid(), "autoarm")`; ErrHeld: exit 0 immediately (another firing owns this Stop). `defer lock.ReleaseNamed(state, ".claude-autoarm.lock")`.
5. `epoch, err := supervise.NextEpoch(state)` (outcome `arming`); on error skip the epoch ledger for this firing and continue, because an unwritable ledger is not proof of anything and every later `SetOutcome` on this path is best-effort.
6. Attempt loop, `AutoarmAttempts` bounded: `reason, err := watch.Run(watch.ConfigFromEnv(h))`.
   Actionable reason (regexp `^(signal:|stale:|check:|heartbeat($|:))`): ACTIONABLE, break.
   ANY other outcome - an error, including `errors.Is(err, lock.ErrHeld)`, or an empty reason with a nil error - consults `supervise.WatcherHealthy(state, GuardGrace)`: healthy means another watcher already owns this home and is still beating inside the shared grace window, so HEALTHY, break; otherwise strike and continue. Keying HEALTHY on the empty-reason return alone makes it dead code, because the identity-bearing singleton steals only from a dead holder, while the reachable case (a live foreign watcher returning ErrHeld) would be escalated into a false failure alarm on a home whose supervision is fine.
7. Outcomes, exactly one:
   - Need vanished (a re-read of `Needed` reports false now, or reports an error, which is the same fail-open exit): `SetOutcome clean`, exit 0.
   - HEALTHY: `ResetBudget`, `SetOutcome clean`, exit 0.
   - ACTIONABLE: `supervise.ResetBudget` (a watcher that closed on a real event proves the failure episode is over, and without this the notified and alarmed markers survive a full recovery and silence every future failure banner), `SetOutcome rewake`, stderr banner below, exit 2.
   - Failure, first in episode (`!NotifiedOnce`): `MarkNotified`, `SetOutcome failed`, stderr failure banner below, exit 2.
   - Failure, repeat (`NotifiedOnce`) and NOT `AlarmFired`: `SetOutcome failed-suppressed`, exit 2 with EMPTY stderr (the silent retry keeps Stop-owned recovery alive without renotifying; upstream contract).
   - Failure, repeat with `AlarmFired`: `SetOutcome failed-suppressed`, exit 0 SILENT. The synchronous guard has already spent this episode's attended fail-open, and a second exit-2 continuation from this hook would defeat it, so the ONE turn the fail-open was meant to release would never end either. It releases exactly that one turn: later Stops still meet the guard's blind-turn banner, which is the ruled posture, and this arm only makes sure this hook is not the thing blocking the turn the guard just let through.

Rewake banner (verbatim, reason line inserted):

```
cfo watcher wake - one supervision event needs a handling turn now.
<reason>
Run cfo drain, handle what it presents, and acknowledge with the WAKE_ACK_REQUIRED command it prints. Do not run cfo watch manually after an ordinary wake.
```

Failure banner (attempts filled in):

```
cfo auto-arm FAILED after <N> attempt(s): the watcher could not hold this home.
Last error: <the final attempt's error string, or "watcher closed without an actionable reason" when the loop ended on an empty reason>
Supervision is down and needs a repair turn: run cfo doctor, then verify the stop-autoarm hook registration in .claude/settings.json, and check state\.watch.lock for a holder that is not yours.
```

- [ ] **Step 1: Write the failing tests**

`hook_test.go` cases with tiny intervals (`CFO_POLL=1`, `CFO_SIGNAL_GRACE=1`, `CFO_HEARTBEAT=1`, `CFO_CLAUDE_AUTOARM_ATTEMPTS=1`). EVERY case pins the identity gate through `CFO_TEST_ANCESTOR_PID`; the ambient ancestry walk cannot be asserted in-process, because a `go test` binary launched from a Claude Code session has claude.exe about five hops up its ancestry, well inside maxHops 16, so `proc.FindAncestor(os.Getpid(), 16, "claude", "node")` succeeds and any case relying on it finding nothing fails deterministically in the implementer's own environment. The seam is therefore an override with a defined disabled state: when `CFO_TEST_ANCESTOR_PID` is set to a value that is not a live pid, the identity gate FAILS outright and the hook exits 0 without consulting the ambient ancestry at all.
1. `TestAutoarmInertWithoutHarnessAncestor`: primary home with a meta file and `CFO_TEST_ANCESTOR_PID` set to the pid of an exited `cmd /c exit 0` child (`cmd.ProcessState.Pid()`), expect exit 0 with both streams empty. This doubles as the manual-invocation safety test: a shell invocation with no harness above it must never arm.
2. `TestAutoarmExitsAtNeedGate`: primary home, no metas, ancestor override set: exit 0, epoch outcome absent, because the need gate exits before the epoch is ever taken. The step-7 `clean` outcomes are covered separately: the HEALTHY arm by `TestAutoarmHealthyAfterSteal`, and the need-vanished arm by `TestAutoarmCleanWhenNeedVanishes` (meta present at start, deleted during the watcher's first wait: exit 0, epoch outcome `clean`).
3. `TestAutoarmRewakeOnSignal`: meta present, status file appended by a goroutine after 300ms: exit 2, stderr contains `cfo watcher wake` and a `signal:` line, epoch outcome `rewake`, exactly one wake record pending.
4. `TestAutoarmHeartbeatIsActionable`: meta present, no changes, heartbeat 1s: exit 2, stderr contains `heartbeat`.
5. `TestAutoarmSingleFlight`: hold `.claude-autoarm.lock` for a live foreign pid with session `autoarm`; expect exit 0 fast.
6. `TestAutoarmFailureEpisode`: force `watch.Run` to fail by pre-holding `.watch.lock` for a live foreign process, with NO `state\.last-watcher-beat` present (a fresh beat would make step 6 read the ErrHeld return as HEALTHY, which is exactly what case 7 arranges): first run exit 2 with `FAILED after 1 attempt(s)` and notified marker created, epoch `failed`; second run exit 2 with EMPTY stderr and epoch `failed-suppressed`.
7. `TestAutoarmHealthyAfterSteal`: primary home with a meta, a pre-written budget count, a live foreign holder of `state\.watch.lock` and a fresh `state\.last-watcher-beat`; expect exit 0, epoch outcome `clean`, the budget file removed, and no notified marker created. This is the only case that exercises the HEALTHY arm, and with the step-6 restatement it is reached through the ErrHeld path rather than the near-unreachable lock-stolen path.
8. `TestAutoarmYieldsToAlarm`: primary home with a meta, the alarmed marker and the notified marker both present, and `state\.watch.lock` pre-held by a live foreign process so the attempt loop can only fail; expect exit 0 with EMPTY stderr. This is the case that proves this hook does not block the one turn the guard's attended fail-open released; it says nothing about later Stops, which keep meeting the blind-turn banner.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/cfo -v`
Expected: FAIL (missing case and helpers).

- [ ] **Step 3: Implement**

The stop-autoarm case lives in hook.go as `hookStopAutoarm(h home.Home, payload claudehook.Payload, stdout, stderr io.Writer) int` with the seven-step sequence commented against upstream (`bin/fm-claude-stop-autoarm.sh` analogues).
`CFO_TEST_ANCESTOR_PID` is read only when set and validated with `proc.Ancestry(pid, 1)`: a pid the Toolhelp snapshot no longer resolves yields an EMPTY walk, which disables the override and fails the identity gate, and so does a pid whose creation time cannot be resolved (an elevated or foreign-user process), because that is a walk stop condition too. `internal/lock` exports no pid-keyed liveness check at all, only the `(*Info).Alive()` method over a previously recorded Start, so there is nothing there for this to call. Document the variable in a comment as a test seam, not a contract.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 5: E2E verification**

Build; fixture primary home with `state\g1.meta` and NO status file yet; `CFO_TEST_ANCESTOR_PID=$PID` (current PowerShell pid), tiny intervals. Arrange stdin explicitly rather than relying on an inherited console handle, and give it a REAL payload rather than an empty file, because `claudehook.ReadPayload` returns ok false on empty input and the hook would fail open at step 1 and exit 0 silent: `Set-Content "$fix\stdin.json" '{"session_id":"s1"}'`, then `Start-Process -FilePath .\cfo.exe -ArgumentList "hook","stop-autoarm" -PassThru -NoNewWindow -RedirectStandardInput "$fix\stdin.json" -RedirectStandardError "$fix\autoarm-err.txt" -RedirectStandardOutput "$fix\autoarm-out.txt"`. Wait until `state\.last-watcher-beat` appears (with the same deadline and 500ms settle the Task 8 e2e uses), then create `state\g1.status`, then `Wait-Process -Id $p.Id -Timeout 30 -ErrorAction SilentlyContinue`.
Expected: exit code 2, stderr file contains `cfo watcher wake` and the `signal:` reason, `.\cfo.exe drain` (same CFO_HOME) shows the pending signal and the WAKE_ACK_REQUIRED line; then `cfo drain --ack-through <seq> --recovery-generation <gen>` empties it.
This is the full arm-wake-drain-ack cycle e2e, no Claude needed.
Delete `.\cfo.exe`.

- [ ] **Step 6: Full-suite check and commit**

```powershell
git add cmd/cfo
git commit -m "feat(hook): stop-autoarm hosts the watcher with asyncRewake contract"
```

---

### Task 12: session-start digest and routing

**Files:**
- Create: `internal/digest/digest.go`
- Test: `internal/digest/digest_test.go`
- Modify: `cmd/cfo/hook.go` (session-start case)
- Modify: `cmd/cfo/main.go` (`session-start` alias command + usage)
- Modify: `cmd/cfo/hook_test.go`, `cmd/cfo/main_test.go`
- Modify: `internal/wake/wake.go` (export the drain renderer as `wake.Render(w io.Writer, records []Record, ep Episode) error` and reuse it from cmd/cfo/drain.go)

**Interfaces:**
- Consumes: `lock.AcquireOwner`/`HeldBy`, `proc.FindAncestor`, `wake.Pending`/`ReadEpisode`/`Render` (the dedup fold now lives inside Render, so Compose never calls `Deduped` itself), `state.ReadMeta`/`TailStatus`, `fsx.ReadLines`, `home`, `claudehook` env.
- Produces:
  - `digest.Compose(h home.Home, ownerPID int, session string, w io.Writer) error`: writes the full digest in this exact section order, each section beginning with a `== <NAME> ==` header line:
    1. `== SESSION LOCK ==`: the AcquireOwner result. On success print `SESSION LOCK: held by pid <ownerPID> on <hostname>`. On ANY acquire error - `lock.ErrHeld` (a different live owner), `lock.ErrOwnerDead` (the resolved harness pid is already gone, a branch Task 4 shipped), or an I/O failure - print this banner verbatim and continue composing READ-ONLY:

    ```
    ●━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
    ●  READ-ONLY DIGEST - THIS SESSION DOES NOT HOLD THE HOME
    ●  Custody: pid <holder pid, or unknown> on <holder host, or unknown> (<error>).
    ●  Every mutating step below is skipped: no lock is taken, no marker is written, no wake is acknowledged.
    ●━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
    ```
    2. `== WAKE QUEUE ==`: `wake.Render` of the RAW pending records and the episode; read-only, never acks. Render takes raw pending records, folds them with `Deduped` for the rows, and takes the ack-through sequence from the raw maximum, so neither call site has to decide what to pass. A write error from Render propagates as the Compose-level failure named in the exit-code paragraph below, and `runDrain` maps the same non-nil return to a nonzero exit.
    3. `== SUPERVISION OPERATING INSTRUCTIONS ==`: fixed text block stating: the Stop-owned auto-arm owns watcher continuity; never run `cfo watch` from the agent shell; wakes arrive as rewake turns; every drain presentation ends with a WAKE_ACK_REQUIRED command that must be run after handling; supervision is needed whenever tasks are in flight.
    4. `== READ-ONCE CONTRACT ==`: fixed text naming every file this digest prints in full (backlog, metas, status tails, projects, overlord, learnings) and forbidding re-reading them this turn.
    5. `== FLEET STATE ==`: backlog compact listing (first QueuedLimit unchecked `- [ ]` lines of `data\backlog.md` plus a `(+N more queued)` overflow line; done rows never listed; `ABSENT` when no file), then every `state\*.meta` (full key=value contents) with a StatusTail-line status tail, then orphan `.status` files (status without meta), listed by name only with NO tail, since an orphan has no goblin to attribute the lines to, then `(no goblins in flight)` when there are no metas.
    6. `== CONTEXT ==`: `data\projects.md`, `data\overlord.md`, `data\learnings.md`, each printed in full or as `<name>: ABSENT` / `<name>: (present, empty)`.
    7. `== NEXT STEP ==`: fixed two-line reminder to follow the supervision block; states the digest never arms anything itself.
    On success under a held lock, write `state\.session-start-complete` containing the owner pid (atomic).
  - Hook routing for `cfo hook session-start` by payload Source. `startup`, `new`, `clear`, `compact`, empty, or unrecognized: full Compose, one code path, no marker consulted, no branch with identical arms. `resume`, `reload`, `fork`: print exactly `CFO: operational input may be waiting; run cfo drain if supervision was active.` and nothing else, but ONLY when `state\.session-start-complete` names the current owner pid and `lock.HeldBy(h.State, ownerPID)` confirms that owner still holds the home; otherwise fall through to full Compose, because a session resuming into a home that never received a digest under this custody would otherwise start blind. That condition is the marker's only consumer and the reason it is written at all.
    SessionStart output is PLAIN TEXT on stdout with exit 0, never the `{"systemMessage":"..."}` envelope the Global Constraints describe for an informational allow. This is a sanctioned exception to that bullet, and it is load-bearing: Claude Code injects SessionStart stdout into the session context verbatim, so a JSON wrapper would deliver the whole digest as one escaped blob, and the substring assertions below would not catch it because `== SESSION LOCK ==` survives inside an escaped blob.
    ALWAYS exit 0: Compose errors are printed as digest text (`SESSION START DEGRADED: <err>`), never as a nonzero exit (a SessionStart exit 2 would block session init). The two error classes are distinct and both are covered. A per-file read failure (a path that exists but cannot be read as text, for example a directory where a file is expected) renders inline as `<name>: UNREADABLE (<err>)` and composition continues with every remaining section; `ABSENT` stays reserved for a path that does not exist. `SESSION START DEGRADED` is reserved for a Compose-level failure - a write error on the output stream, or a home that cannot be resolved - and is the last line written.
  - `cfo session-start` (no `hook` prefix): manual alias, full Compose, exit 0.
  - Owner resolution for the hook: `proc.FindAncestor(..., "claude", "node")`; when absent (manual invocation), fall back to `os.Getpid()` so the manual alias still works. Owner resolution honors the same `CFO_TEST_ANCESTOR_PID` override Task 11 defines, with the same disabled state, so the routing tests can pin the owner pid instead of guessing which claude.exe the ambient walk will find.

- [ ] **Step 1: Write the failing digest tests**

`digest_test.go` against fixture homes:
1. `TestComposeSectionOrder`: full fixture (backlog with 3 queued + 1 done, two metas, one orphan status, all three context files, two appended wake records and one published episode); output contains the seven `== ... ==` headers in order (assert via successive `strings.Index` comparisons), and the WAKE QUEUE section contains the WAKE_ACK_REQUIRED line carrying the fixture's actual maximum sequence and generation, so the shared renderer is inspected here as well as in drain.
2. `TestComposeBacklogCompact`: a backlog of 25 unchecked `- [ ]` rows plus 3 checked `- [x]` rows with distinctive text, at QueuedLimit 20; assert exactly 20 queued rows, the exact string `(+5 more queued)`, and that none of the three done rows' text appears anywhere in the output.
3. `TestComposeAbsentContext`: missing overlord.md prints `overlord.md: ABSENT`.
4. `TestComposeReadOnlyOnHeldLock`: lock pre-held by a live foreign owner; output contains the banner's exact second line (`●  READ-ONLY DIGEST - THIS SESSION DOES NOT HOLD THE HOME`) and the holder's pid, and `.session-start-complete` is NOT written.
5. `TestComposeWritesCompleteMarker`: unheld lock; marker exists and contains the owner pid.
6. `TestComposeStatusTailCap`: `g1.meta` plus a `g1.status` of 12 distinct lines at the StatusTail default of 5; assert the last 5 lines appear and the 7th-from-last does not. A second fixture with `orphan.status` and no matching meta asserts the name appears and none of its lines do.
7. `TestComposeRendersUnreadableFilesInline`: create `data\projects.md` as a DIRECTORY; Compose still emits all seven section headers in order, the CONTEXT section shows `projects.md: UNREADABLE`, the hook exits 0, and `SESSION START DEGRADED` does NOT appear (a single bad file degrades one line, not the digest).

Routing tests in `hook_test.go`:
8. Source `resume` with `.session-start-complete` naming the live lock owner: exit 0, stdout is exactly the nudge line. Pin that owner with `CFO_TEST_ANCESTOR_PID` set to a live pid the test controls (the ping-child pattern), because the ambient `FindAncestor` walk succeeds inside the implementer's own Claude session and returns a claude.exe pid the fixture cannot predict; with the pid pinned, the marker and the lock record are pre-written to match it. Source `resume` with no marker: exit 0, stdout contains `== SESSION LOCK ==` (the fall-through that keeps a resumed session from starting blind). Sources `reload` and `fork` with the marker present: the byte-exact nudge line. An unrecognized source such as `banana`: full Compose.
9. Source `startup`: exit 0, stdout contains `== SESSION LOCK ==`.
10. Not primary: exit 0 silent.
11. `main_test.go` row: `session-start` alias dispatches (wantExit 0, wantStdout `== SESSION LOCK ==`, env fixture home).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/digest ./cmd/cfo -v`
Expected: FAIL to build.

- [ ] **Step 3: Implement**

`digest.go` is sequential section writers over one `io.Writer`; every file read is CRLF-tolerant via `fsx.ReadLines`; no subprocesses anywhere in the package (the 1s budget is met by construction; the upstream 120s watchdog is NOT PORTED IN V1 because no network or subprocess stage exists, comment says so).
`wake.Render` extraction: move the drain formatting verbatim from cmd/cfo/drain.go into the wake package and call it from both sites. Task 5's line-by-line rendering test is what makes "no format change" a checkable claim rather than a hope; land that test before this extraction and keep it pointed at `runDrain` so both call sites stay covered.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/digest ./internal/wake ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 5: E2E verification with the 1s budget**

Build; fixture primary home with two metas, statuses, backlog, and context files; run:

```powershell
$sw = [Diagnostics.Stopwatch]::StartNew()
'{"session_id":"s","source":"startup"}' | .\cfo.exe hook session-start | Out-File "$fix\digest.txt"
$code = $LASTEXITCODE
$sw.Stop(); "elapsed ms: $($sw.ElapsedMilliseconds)  exit: $code"
if ($code -ne 0) { throw "session-start must always exit 0, got $code" }
```
Expected: exit 0, digest.txt shows all seven sections in order, elapsed under 1000ms.
Then `Remove-Item "$fix\state\.lock"` and pipe `{"source":"resume"}` into the same fixture. Deleting the lock record is what forces the fall-through, and it has to be deliberate: the marker does NOT name the exited cfo.exe process, it names the resolved harness ancestor, which is the still-live PowerShell or claude.exe above it, so `lock.HeldBy` would otherwise be TRUE and this leg would print the nudge instead. With no lock record there is no custody evidence at all. Expected: exit 0 with `== SESSION LOCK ==` on stdout, which is the resumed-session-must-not-start-blind path. The one-line nudge is exercised by the routing test instead, where one pinned owner spans both calls.
Delete `.\cfo.exe`.

- [ ] **Step 6: Full-suite check and commit**

```powershell
git add internal/digest internal/wake cmd/cfo
git commit -m "feat(hook): session-start digest with source routing"
```

---

### Task 13: settings wiring and the whole-family e2e proof

**Files:**
- Modify: `.claude/settings.json`
- Create: `cmd/cfo/e2e_hooks_test.go`
- Modify: `internal/home/home.go` (collapse the unexported `gitPath` pair into one `rev-parse` spawn; IsPrimary's exported contract is unchanged)
- Modify: `docs/superpowers/specs/2026-08-12-windows-native-fork-design.md` is NOT touched (spec stays frozen); the wiring snippet lives in settings.json itself.

**Interfaces:**
- Consumes: everything Tasks 1-12 shipped.
- Produces: the live hook wiring and a repeatable proof that the whole family honors its contracts from a real binary.

- [ ] **Step 1: Write the failing e2e test**

`cmd/cfo/e2e_hooks_test.go`: a single `TestHookFamilyEndToEnd` that:
- Builds the real binary once into `t.TempDir()` with `exec.Command(goBin, "build", "-o", exe, "./cmd/cfo")` and `cmd.Dir` set to the repo root (`filepath.Join(wd, "..", "..")` from `cmd/cfo`), resolving `goBin` from `runtime.GOROOT()\bin\go.exe` and `t.Skip`ping only when the toolchain is absent. Assert on `CombinedOutput` and fail the test with that output when the build errors, so a build failure surfaces its own error text rather than a confusing downstream assertion. The package pattern must be `./cmd/cfo`: `go build -o <file> ./...` is rejected outright, because the pattern matches many packages and the `-o` target is not a directory.
- Creates a fixture primary home (AGENTS.md, `state\`, git init) and a bare dev home (no `state\`).
- Table of seven invocations against the PRIMARY home, each asserting exit code, stdout shape, stderr shape:
  1. `hook session-start` with `{"source":"startup"}`: exit 0, stdout has the seven section headers.
  2. `hook pretool-subagent` with tool_name Agent: exit 2, stderr envelope, empty stdout.
  3. `hook pretool-arm` with command `cfo watch &`: exit 2, code watcher-background.
  4. `hook pretool-cd` with command `cd C:\`: exit 2, code cwd-relocation.
  5. `hook turnend-guard` with one meta present: exit 2, `TURN WOULD END BLIND` (set CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS=1).
  6. `hook stop-autoarm` with a meta, tiny intervals, CFO_TEST_ANCESTOR_PID set, and a status append goroutine: exit 2, `cfo watcher wake`.
  7. `hook pretool-arm` with command `git log --oneline`: exit 0, empty stdout, empty stderr.
- INERTNESS PROOF against the DEV home: all seven invocations exit 0 with empty stdout AND stderr, and a recursive directory listing before and after is IDENTICAL (no file or directory created anywhere in the home). This assertion is the plan's inert-means-inert guarantee, mechanically enforced. The loop asserts its own execution: count the invocations it made and fail unless it ran all seven commands, so an empty or short-circuited loop cannot report success. Capture the recursive listing as a sorted slice of relative paths and compare with `reflect.DeepEqual`, reporting the exact added path on failure rather than only a count mismatch.
- Cases 1, 5 and 6 each mutate home state and get their own fixture primary home; cases 2, 3, 4 and 7 are stateless reads and may share one. Do not add `t.Parallel` to this table, and do not rely on declaration order for correctness.

- [ ] **Step 2: Run it to verify current state**

Run: `go test ./cmd/cfo -run TestHookFamilyEndToEnd -v`
Expected: PASS already if Tasks 1-12 landed correctly; treat any failure as a real integration defect to fix now (this task has no new production code besides wiring).

- [ ] **Step 3a: Determine the hook shell, and record the answer in this task**

Register one temporary SessionStart hook whose command is `echo shell=$0 argv=$@ > cfo-hookshell.txt`, start a throwaway Claude session in the repo, and read `C:\dev\code-goblins\cfo-hookshell.txt`. The redirect target is deliberately relative, because hooks run with the working directory at the project root and both candidate shells resolve a bare name identically, while `%TEMP%` is expanded by only one of them: under a POSIX shell the redirect would create a file literally named `%TEMP%\cfo-hookshell.txt` in the project directory, reading `$env:TEMP\cfo-hookshell.txt` would find nothing, and the missing file would be scored as the cmd.exe verdict. A file containing `shell=/bin/sh` or a similar POSIX `$0` expansion proves hook commands go through a POSIX shell; a file containing the literal text `shell=$0` proves they go through `cmd.exe`. Write the verdict into this task as a one-line note before continuing, because Step 3b picks its command form from it and every hook in the family is dead if the guess is wrong. Delete the probe file and the temporary hook registration afterwards.

- [ ] **Step 3b: Wire .claude/settings.json**

If Step 3a found a POSIX shell, replace the file's `{}` with exactly:

```json
{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "[ -x \"$CLAUDE_PROJECT_DIR\"/cfo.exe ] || exit 0; \"$CLAUDE_PROJECT_DIR\"/cfo.exe hook session-start", "timeout": 120}]}
    ],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [
        {"type": "command", "command": "[ -x \"$CLAUDE_PROJECT_DIR\"/cfo.exe ] || exit 0; \"$CLAUDE_PROJECT_DIR\"/cfo.exe hook pretool-arm"},
        {"type": "command", "command": "[ -x \"$CLAUDE_PROJECT_DIR\"/cfo.exe ] || exit 0; \"$CLAUDE_PROJECT_DIR\"/cfo.exe hook pretool-cd"}
      ]},
      {"matcher": ".*", "hooks": [
        {"type": "command", "command": "[ -x \"$CLAUDE_PROJECT_DIR\"/cfo.exe ] || exit 0; \"$CLAUDE_PROJECT_DIR\"/cfo.exe hook pretool-subagent"}
      ]}
    ],
    "Stop": [
      {"hooks": [
        {"type": "command", "command": "[ -x \"$CLAUDE_PROJECT_DIR\"/cfo.exe ] || exit 0; \"$CLAUDE_PROJECT_DIR\"/cfo.exe hook turnend-guard"},
        {"type": "command", "command": "[ -x \"$CLAUDE_PROJECT_DIR\"/cfo.exe ] || exit 0; \"$CLAUDE_PROJECT_DIR\"/cfo.exe hook stop-autoarm", "asyncRewake": true, "timeout": 28800}
      ]}
    ]
  }
}
```

If Step 3a found `cmd.exe`, use the same structure with every command written in the cmd form instead, which is a single statement with no chaining: `if exist "%CLAUDE_PROJECT_DIR%\cfo.exe" "%CLAUDE_PROJECT_DIR%\cfo.exe" hook session-start`, and likewise for the other five names, keeping `"timeout": 120` on SessionStart and `"asyncRewake": true, "timeout": 28800` on stop-autoarm.
Either guard makes every hook a silent no-op until a `cfo.exe` sits at the repo root (git-ignored), so dev sessions stay clean; `install.ps1` (Plan 5) owns wiring for installed homes.
The four events and six commands mirror upstream's registration one-to-one, `asyncRewake` and the 8h timeout preserved verbatim.

- [ ] **Step 3c: Execute the registered command string, not just the binary**

For each of the six registered commands, run the EXACT string through the shell Step 3a identified, once against the fixture primary home and once against the dev home, asserting the same exit codes and stream shapes the e2e table asserts for the direct invocations. This is the only step that exercises the artifact users actually get; the seven-row table above exercises the binary, which is not the same thing.

- [ ] **Step 4: Collapse the IsPrimary git probe and measure every hook**

`home.gitPath` currently spawns `git rev-parse` twice. Replace the pair with one `git -C h.Root rev-parse --git-dir --git-common-dir` invocation reading its two output lines in order, keeping IsPrimary's contract identical (the two paths are compared with `strings.EqualFold` on cleaned absolute paths exactly as before). Measured on the target machine, the pair costs 64ms per IsPrimary call and the single invocation costs 31.5ms; Task 1's shipped tests are unchanged by this and must stay green.
Then measure. Add to the e2e table a timing assertion per hook: loop `hook pretool-arm`, `hook pretool-cd`, `hook pretool-subagent` and `hook session-start` twenty times each against the PRIMARY fixture home, report the median, and fail when a PreToolUse hook exceeds the 150ms Global Constraints budget or the digest exceeds 1s.
The Global Constraints budget's composition, restated here because this is the step that has to reproduce it: the dominant cost is `home.IsPrimary`, whose git probe measures 64ms as the two separate `rev-parse` spawns Task 1 shipped and 31.5ms after the collapse above, and Step 3b registers three PreToolUse hooks that a Bash matcher fires together, so a single Bash tool call pays that cost three times. `turnend-guard` adds up to SyncWaitMS (default 800ms) on top whenever it has to wait for the sibling auto-arm's proof, and `stop-autoarm` is unbounded by design because it hosts the watcher. The inert path stays cheap in any case: IsPrimary stats AGENTS.md and state/ before it ever shells out, so a dev checkout never pays the git probe.

- [ ] **Step 5: Full verification battery**

Run: `go vet ./...` then `go test ./... -count=1` then the e2e test once more.
Expected: everything green.

- [ ] **Step 6: Commit**

```powershell
git add .claude/settings.json cmd/cfo/e2e_hooks_test.go internal/home
git commit -m "feat(hook): wire claude settings and whole-family e2e proof"
```

---

## Self-Review Notes

- Spec coverage: section 4's four events and six hook commands map to Tasks 6, 7, 10, 11, 12 with wiring in Task 13; section 2's watcher maps to Tasks 8 and 9 (fs notifications + timers, zero idle CPU via blocking waits, one-shot triage); the exit-code contract and stdout/stderr duality live in Task 2 and are enforced end-to-end in Task 13; the per-hook timing budget is measured in Task 13 step 4 (150ms per PreToolUse hook, under 1s for the digest, which is also timed in Task 12's e2e step), and the same step collapses IsPrimary's git probe from two spawns to one so the budget is reachable.
- Sanctioned deviations from upstream, each marked in code comments: wake queue keeps Plan 1's JSON-lines storage while porting the kind whitelist, key-based dedup, detail-text conventions, and the recovery-generation episode marker (the documented drain contract is preserved; the byte format is not, because no bash reader survives the port); the episode marker drops upstream's second `handling` phase, which nothing in this plan produces; the cd-guard shares IsPrimary instead of its looser upstream predicate; SessionStart emits plain text on stdout rather than the informational-allow JSON envelope, because Claude Code injects that stream into the session context verbatim; Task 4 shipped `lock.ErrOwnerDead` and a zero recorded `Start` for an unverifiable owner, so liveness is always the lock record plus a fresh beat and never the record alone; the same-epoch budget dedup and the 120s digest watchdog are not ported (no multi-fire-per-Stop and no subprocess stages exist to need them); AFK, gate-agent refusal, network stage, check sweeps, staleness, procevent, and X-mode are v1 cuts per spec section 9.
- Type consistency: `home.Home` flows into every hook; `lock.Info` gains Session/OwnerPID in Task 4, and the named-lock family (`AcquireNamedOwner`, `ReadNamed`, `HeldByNamed`, `ReleaseNamed`) lands whole in Task 5 as the earliest consumer, before Tasks 8, 10 and 11 use it; `wake.Append`'s four-argument signature and the phase-free `PublishEpisode(dir)` land in Task 5 before Tasks 8, 11 and 12 consume them; `watch.Config`'s WaitEvent and Cleanup land nil-able in Task 8 and are wired together in Task 9; `watch.ScanSignals` returns `[]Change` and pairs with `CommitSignatures` from Task 8 onward.
- The plan's tests specify behavior contracts precisely but Tasks 2, 3, 5, 9-12 leave implementation bodies to the implementer within stated constraints; reviewers hold the contract tables above as the spec.

