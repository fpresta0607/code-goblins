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
- Hook exit-code contract: PreToolUse deny is exit 2 with a one-line stderr JSON envelope `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"},"systemMessage":"..."}` and empty stdout; Stop block/rewake is exit 2 with plain stderr text and empty stdout; informational allow is exit 0 with a stdout JSON `{"systemMessage":"..."}`; every other outcome is exit 0 and silent. Transport failures (bad stdin, missing fields) fail open: exit 0, silent.
- Environment variables use the `CFO_` prefix (upstream `FM_*` names are not read); defaults carry upstream's exact numbers.
- Naming in all output: the human is the Supreme Overlord, the primary agent is the CFO, workers are Code Goblins. No em dash characters in any output or doc.
- Performance: every hook except `stop-autoarm` completes in under 50ms on the target machine; the session-start digest completes in under 1s.
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
| SessionStartTimeout | CFO_SESSION_START_TIMEOUT | 120s |
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
internal/lock/lock.go              MODIFY: owner-acquire (harness pid + session id)
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
- Stop the walk when: pid missing from the snapshot, ParentPID is 0, hop limit reached, or the parent's creation time is after the child's (PID reuse).
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
  - Existing `Acquire(dir)` becomes a one-line wrapper: `AcquireOwner(dir, os.Getpid(), "")`.
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
```

`localHostnameForTest` mirrors the existing tests' hostname helper; reuse whatever helper name the file already has.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/lock -v`
Expected: FAIL to build with `undefined: AcquireOwner`.

- [ ] **Step 3: Implement**

Refactor inside `internal/lock/lock.go` only:
- `selfInfo()` becomes `ownerInfo(pid int, session string) *Info` (self is `ownerInfo(os.Getpid(), "")`); it queries `processStart(pid)` and records `PID: pid, OwnerPID: pid, Session: session`.
- The Acquire loop body is extracted unchanged into `acquire(dir string, self *Info) (*Info, error)`; `Acquire` and `AcquireOwner` both call it.
- The idempotent self-match inside the loop compares `holder.PID == self.PID && holder.Start.Equal(self.Start) && holder.Hostname == self.Hostname` exactly as today (Session deliberately excluded; add the comment "a resumed session keeps custody").
- `HeldBy` reads the record, requires hostname match, PID match, and a live `processStart(ownerPID)` within the same one-second tolerance `Alive` uses.
- Doc comments updated: the lock records CUSTODY of a long-lived owner process (the harness), not the caller.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/lock -v`
Expected: PASS (all prior tests plus the four new ones).

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

**Interfaces:**
- Consumes: `fsx.ReadLines`, `fsx.AtomicWriteFile` (existing).
- Produces:
  - `wake.Record` gains `Key string` with JSON tag `key` (additive; old lines unmarshal with empty Key).
  - `wake.Append(dir, kind, key, detail string) (Record, error)` SIGNATURE CHANGE from Plan 1's three-argument form; `kind` must be one of `signal`, `stale`, `check`, `heartbeat` or Append returns an error naming the whitelist. All existing wake tests are updated mechanically to pass a key (use the kind as key where the old tests passed only kind+detail).
  - `wake.Deduped(records []Record) []Record`: presentation fold, last-write-wins per `(kind,key)` preserving first-seen order of surviving buckets; ALL `heartbeat` records collapse into one bucket regardless of key.
  - `wake.PublishEpisode(dir, phase string) (int, error)`: phase is `downtime` or `handling`; increments the generation and atomically writes `state/.watcher-down` as one line `pending:<phase>:<gen>`.
  - `wake.ReadEpisode(dir string) (Episode, error)` with `type Episode struct { Pending bool; Phase string; Gen int }`; missing file returns zero Episode, nil error.
  - `wake.AckEpisode(dir string, gen int) error`: retires a pending episode whose generation matches by rewriting the line as `acked:<phase>:<gen>`; a generation mismatch returns `wake.ErrGenerationMismatch` (callers treat it as re-drain, not failure).
  - `cfo drain [--ack-through <seq>] [--recovery-generation <gen>]`: with no flags, prints the deduped pending queue and any pending episode, then the exact ack command line; with flags, performs the acks. Exit 0 in every non-error case, including generation mismatch (which prints `recovery generation moved, re-run: cfo drain`).

Drain output format, exact:

```
WAKE QUEUE: 3 pending
  2  signal  g1.status: signal:g1.status
  5  stale   w1: stale: w1 (idle 300s)
  7  heartbeat  heartbeat
RECOVERY EPISODE: pending:downtime generation 4
WAKE_ACK_REQUIRED: cfo drain --ack-through 7 --recovery-generation 4
```

When the queue is empty and no episode is pending, print exactly `WAKE QUEUE: empty` and exit 0.
Detail text conventions (`signal:<paths>`, `stale: <window> (...)`, `check: <script>: <out>`, `heartbeat`) are the crew-facing contract; the watcher tasks emit them verbatim.

- [ ] **Step 1: Write the failing tests**

Update `wake_test.go`: adapt every existing call to the four-argument Append, then add:
1. `TestAppendRejectsUnknownKind`: `Append(dir, "bogus", "k", "d")` errors; the error text contains all four legal kinds.
2. `TestDedupedLastWriteWinsPerKindKey`: append `signal/a`, `signal/b`, `signal/a` (later detail); Deduped returns 2 records, the `a` bucket carrying the later detail and seq.
3. `TestDedupedCollapsesHeartbeats`: three heartbeats with different keys collapse to one record (the latest).

Create `episode_test.go`:
1. `TestPublishIncrementsGeneration`: two publishes yield gens 1 then 2; file holds `pending:downtime:2`.
2. `TestAckMatchingGeneration`: publish then Ack(gen) succeeds; ReadEpisode reports Pending false.
3. `TestAckMismatchReturnsSentinel`: publish gen 1, publish gen 2, `AckEpisode(dir, 1)` returns ErrGenerationMismatch and the file still says `pending:...:2`.
4. `TestReadEpisodeMissingFileIsZero`: empty state dir reads as zero Episode, nil error.

Add a `main_test.go` dispatch row: `{name: "drain empty queue", args: []string{"drain"}, wantExit: 0, wantStdout: "WAKE QUEUE"}` (drain resolves the home from cwd; the test harness sets `CFO_HOME` to a `t.TempDir()` with a `state/` subdir via `t.Setenv` before calling `run`; add that setup support to the table struct as an optional `env map[string]string` field).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/wake ./cmd/cfo -v`
Expected: FAIL to build (signature change plus undefined episode functions).

- [ ] **Step 3: Implement**

`wake.go`: add the whitelist check at the top of Append (`var kinds = map[string]bool{...}`); add Key to Record and to the append path; `Deduped` builds a `map[string]int` from bucket key (`kind+"\x00"+key`, with plain `heartbeat` as the bucket key for every heartbeat) to the index in an output slice, overwriting in place.
`episode.go`: one-line file `state/.watcher-down` parsed with `strings.Split(line, ":")` into status/phase/gen; generation state lives in the same file (max seen gen is the current one); writes go through `fsx.AtomicWriteFile`.
`cmd/cfo/drain.go`: `runDrain(h home.Home, args []string, stdout, stderr io.Writer) int` using the `flag` package with a custom FlagSet; wire `case "drain":` in main.go after resolving home (missing `state/` dir prints `WAKE QUEUE: empty` and exits 0; drain never creates directories).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/wake ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 5: E2E verification**

```powershell
& "C:\Program Files\Go\bin\go.exe" build ./cmd/cfo
$env:CFO_HOME = "$env:TEMP\cfo-e2e-drain"; New-Item -ItemType Directory -Force "$env:CFO_HOME\state" | Out-Null
.\cfo.exe drain
```
Expected: `WAKE QUEUE: empty`, exit 0.
Then remove `$env:CFO_HOME` from the environment and delete `.\cfo.exe`.

- [ ] **Step 6: Full-suite check and commit**

Run: `go vet ./...` then `go test ./...`

```powershell
git add internal/wake cmd/cfo
git commit -m "feat(wake): kind whitelist, dedup, recovery episodes, cfo drain"
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

`subagent_test.go`, table-driven over: `Agent` denies on stem agent; `SendMessage` denies on sendmessage; `TaskCreate` allows (plan-only); `TaskOutput` allows (observe-only); `TaskStop` allows; `mcp__herdr__spawn_task` allows (MCP); `Bash` allows; `Read` allows; `CronCreate` denies on cron; `EnterWorktree` denies on worktree; `Workflow` denies on workflow; case-insensitivity: `AGENT` denies.

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
    Fast path: a command containing neither `fm-watch` nor `cfo watch` (case-insensitive, after stripping backslashes and quotes) and no ANSI-C quoting marker (`$'` or `$"`) allows immediately.
    Deny codes, checked in this order on a watcher-referencing command: `broad-watcher-kill` (contains `pkill`, `taskkill`, or `Stop-Process` in the same command as a watcher token), `watcher-background` (trailing `&` or `Start-Job`/`Start-Process` with a watcher token), `watcher-pipeline` (watcher token inside a `|` segment), `watcher-redirection` (watcher token with `>`, `>>`, or `2>`), `watcher-bundled` (watcher token in a command also containing `&&`, `;`, or `||`), `watcher-nested` (watcher token inside `$(`, backticks, or an `eval`/`bash -c`/`powershell -Command` wrapper), `unclassifiable-protected-command` (ANSI-C markers present with a watcher token), and finally `watcher-direct` for any remaining watcher invocation.
    v1 posture: every watcher-family Bash invocation denies; the repair path for a down watcher is fixing the hook registration, never running the watcher from the agent shell. The specific codes exist for diagnostics parity with upstream.
  - `guard.ClassifyCd(command string) (code, reason string, deny bool)`.
    Denies a command whose FINAL top-level statement is a bare `cd`, `pushd`, `popd`, or `Set-Location` (a persistent cwd relocation); allows `cd` inside `(...)` subshells, allows any command whose final statement is not a relocation (the shell returns), allows `git -C` and `--cwd`-style flags.
    Deny code is always `cwd-relocation`.
  - Hook behavior for both `cfo hook pretool-arm` and `cfo hook pretool-cd`: transport fail-open; `IsPrimary` gate; classify `payload.Command`; deny message is `[<code>] <reason>` through `DenyPreTool`.

- [ ] **Step 1: Write the failing classifier tests**

`armcd_test.go`, table-driven.
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
11. `cfo watchdog-config`, allow (token boundary: `cfo watch` matches only as a word pair; implement with a regex on word boundaries).
12. `git log --oneline`, allow.

Cd cases:
1. `cd C:\other`, deny.
2. `pushd ..`, deny.
3. `cd sub && go test ./...`, allow (final statement is not a relocation).
4. `(cd sub && make)`, allow (subshell).
5. `git -C C:\x status`, allow.
6. `go test ./... ; popd`, deny (final statement relocates).
7. `Set-Location C:\`, deny.
8. `echo cd`, allow (cd is an argument, not a statement head; tokenize on statement separators `&&`, `;`, `||`, `|` and inspect each segment's first word).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/guard -v`
Expected: FAIL to build with `undefined: ClassifyArm`.

- [ ] **Step 3: Implement**

`armcd.go`: both classifiers are pure string analysis (`strings`, `regexp` compiled at package init).
Normalization helper shared by both: lowercase, strip `\` and quote characters for token detection while keeping the original for structure detection (separators, parens).
Keep each classifier under 80 lines; the deny-code ladder is a sequence of if-checks in the documented order, not a table of regexes per code.

- [ ] **Step 4: Wire the hook cases and dispatcher tests**

Add `hook_test.go` cases: arm deny in primary home (payload command `cfo watch &`, expect exit 2 and code `watcher-background` in systemMessage), arm inert without state/, cd deny (`cd C:\`), cd allow (`cd sub && go test`).

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
- Modify: `internal/lock/lock.go` (add `AcquireNamedOwner`)
- Modify: `internal/lock/lock_test.go` (one test)
- Modify: `cmd/cfo/main.go` (add `watch` case and usage line)

**Interfaces:**
- Consumes: `wake.Append`, `wake.PublishEpisode`, `lock.AcquireNamedOwner`, `home.Home`, `claudehook.Seconds` for intervals.
- Produces:
  - `lock.AcquireNamedOwner(dir, name string, ownerPID int, session string) (*Info, error)`: identical to AcquireOwner but the lock file is `dir\<name>` instead of `dir\.lock`. AcquireOwner delegates to it with name `.lock`.
  - `type watch.Config struct { Home home.Home; Poll, SignalGrace, Heartbeat, HeartbeatMax time.Duration; Now func() time.Time; Sleep func(time.Duration); WaitEvent func(timeout time.Duration) bool }` with `watch.ConfigFromEnv(h home.Home) Config` filling defaults (Now/Sleep default to time.Now/time.Sleep; WaitEvent defaults to nil meaning pure timer mode until Task 9 supplies one).
  - `watch.Run(cfg Config) (reason string, err error)`: acquires the singleton `state/.watch.lock`, then loops: touch `state/.last-watcher-beat`; scan signals; on changed status files, sleep SignalGrace, rescan to coalesce, append ONE wake record `(kind signal, key <first changed basename>, detail "signal:<space-joined relative paths>")`, and return that detail as the reason; when the heartbeat is due, append `(heartbeat, heartbeat, "heartbeat")` and return `"heartbeat"`; heartbeat interval doubles after every heartbeat close that followed an unchanged fleet scan, capped at HeartbeatMax, and resets to Heartbeat when any signal fired since.
    Between checks it waits via `WaitEvent(Poll)` when non-nil else `Sleep(Poll)`.
    On any return path except lock-lost, `PublishEpisode(dir, "downtime")` is called BEFORE releasing the singleton (upstream's watcher-down marker); when the singleton was lost to a successor (`lock.HeldBy` on the named lock no longer names us), return `("", nil)` without publishing.
  - `watch.ScanSignals(stateDir string) ([]string, error)`: compares each `*.status` and `*.turn-ended` file in stateDir against a persisted `size:mtime` signature in `state/.seen-<sanitized>` and updates signatures; returns relative names that changed. Signatures persist across watcher restarts (that is the point: signals landing while no watcher runs are caught on the next start).
  - `watch.Sanitize(name string) string`: maps every character outside `[A-Za-z0-9_-]` to `_` (covers `:` `/` `\` `.` which are illegal or ambiguous in NTFS filenames).
  - `cfo watch` subcommand: resolves home; refuses (exit 1, message) when not IsPrimary; otherwise calls Run with env config and prints the reason line to stdout before exiting 0. Intended for manual diagnostics only; the hooks are the production entry.

- [ ] **Step 1: Write the failing tests**

`watch_test.go` (all with `t.TempDir()` state dirs, tiny injected intervals, fake Now/Sleep so no test sleeps for real):
1. `TestSanitize`: `g1.status` becomes `g1_status`; `w:1/2` becomes `w_1_2`.
2. `TestScanSignalsDetectsNewAndChanged`: first scan of a dir with `a.status` reports `a.status` (new file counts as changed); second scan reports nothing; append a line to the file, third scan reports it again.
3. `TestScanSignalsPersistsAcrossRestarts`: scan, then write a change, then call ScanSignals fresh (simulating a new watcher process); the change is detected.
4. `TestRunClosesOnSignal`: fixture with one status file; a fake Sleep that appends a line to the status file on its first invocation; Run returns a reason starting `signal:` containing the filename, exactly one wake record exists with kind signal, and `.watcher-down` reads `pending:downtime:1`.
5. `TestRunClosesOnHeartbeat`: no status changes, Heartbeat set to one Poll tick via fake clock; Run returns `heartbeat` and one heartbeat wake record exists.
6. `TestRunSingletonExcludes`: hold `state/.watch.lock` via AcquireNamedOwner for a live foreign process (the ping-child pattern from the lock tests); Run returns an error wrapping lock.ErrHeld.
7. `TestAcquireNamedOwnerDistinctFiles` (in lock_test.go): AcquireNamedOwner with name `.watch.lock` coexists with Acquire's `.lock` in the same dir.
8. `TestRunTouchesBeat`: after a heartbeat close, `state/.last-watcher-beat` exists with a recent mtime.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/watch ./internal/lock -v`
Expected: FAIL to build.

- [ ] **Step 3: Implement**

`lock.go`: extract the path once (`filepath.Join(dir, name)`) through the existing acquire loop; four-line change plus doc comments.
`watch.go` implementation constraints:
- The loop is single-goroutine; no channels except an optional WaitEvent hook.
- The heartbeat backoff state is local to Run (`interval := cfg.Heartbeat; interval = min(interval*2, cfg.HeartbeatMax)` after a quiet heartbeat; reset on signal).
- Beat touch uses `os.Chtimes` when the file exists, else an empty `os.WriteFile` (no fsx needed for an mtime beacon).
- One wake record per close, appended before the singleton releases; comment: "one actionable reason closes one watcher cycle; continuity is the arm layer's job".
- NOT PORTED IN V1 comments at the stale-scan and check-sweep insertion points naming Plan 3 and Plan 4.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/watch ./internal/lock -v`
Expected: PASS.

- [ ] **Step 5: E2E verification**

```powershell
& "C:\Program Files\Go\bin\go.exe" build ./cmd/cfo
$fix = "$env:TEMP\cfo-e2e-watch"; New-Item -ItemType Directory -Force "$fix\state" | Out-Null
Set-Content "$fix\AGENTS.md" "# home"; git -C $fix init 2>$null
Set-Content "$fix\state\g1.status" "spawned"
$env:CFO_HOME = $fix; $env:CFO_POLL = "1"; $env:CFO_SIGNAL_GRACE = "1"
$p = Start-Process -FilePath .\cfo.exe -ArgumentList "watch" -PassThru -NoNewWindow -RedirectStandardOutput "$fix\watch-out.txt"
Start-Sleep 2; Add-Content "$fix\state\g1.status" "working"; Wait-Process -Id $p.Id -Timeout 15
Get-Content "$fix\watch-out.txt"; .\cfo.exe drain
```
Expected: the watch process exits on its own within the timeout, `watch-out.txt` contains a `signal:` line naming `g1.status`, and `cfo drain` shows one pending signal record plus a pending downtime episode with the WAKE_ACK_REQUIRED line.
Clean up env vars and `.\cfo.exe`.

- [ ] **Step 6: Full-suite check and commit**

```powershell
git add internal/watch internal/lock cmd/cfo
git commit -m "feat(watch): triage loop with signal scan, heartbeat, singleton"
```

---

### Task 9: filesystem notifications with a permanent-degrade circuit breaker

**Files:**
- Create: `internal/watch/notify_windows.go`
- Test: `internal/watch/notify_windows_test.go`
- Modify: `internal/watch/watch.go` (ConfigFromEnv wires WaitEvent)

**Interfaces:**
- Consumes: stdlib `syscall` only.
- Produces:
  - `watch.NewDirWaiter(dir string) (*DirWaiter, error)`: opens the directory with `syscall.CreateFile(..., FILE_LIST_DIRECTORY, FILE_SHARE_READ|WRITE|DELETE, nil, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OVERLAPPED, 0)` and an event handle for overlapped completion.
  - `(*DirWaiter).Wait(timeout time.Duration) bool`: issues `syscall.ReadDirectoryChanges` for `FILE_NOTIFY_CHANGE_FILE_NAME|LAST_WRITE|SIZE`, waits with `syscall.WaitForSingleObject(event, ms)`, returns true on a change event and false on timeout.
    Any API failure counts one breaker strike; after `EventCapFailMax` (default 3) CONSECUTIVE failures the waiter permanently degrades for its lifetime: Wait becomes `time.Sleep(timeout); return false`. A success resets the strike counter. Degradation is one-way by design (upstream's fail-closed-to-slow-but-correct posture; Windows directory watches silently die under AV filter drivers).
  - `(*DirWaiter).Degraded() bool` for tests and logs; `(*DirWaiter).Close()`.
  - `watch.ConfigFromEnv(h home.Home) Config` now attempts `NewDirWaiter(h.State)`; on success wires `cfg.WaitEvent` to `waiter.Wait`; on failure leaves WaitEvent nil (pure timer mode). Either way it fills every interval from the env table.

- [ ] **Step 1: Write the failing tests**

`notify_windows_test.go`:
1. `TestWaitSeesFileWrite`: waiter on a temp dir; goroutine writes a file after 100ms; `Wait(5*time.Second)` returns true in well under 3s (assert elapsed).
2. `TestWaitTimesOutQuietly`: `Wait(200*time.Millisecond)` on an untouched dir returns false, elapsed at least 150ms.
3. `TestBreakerDegradesAfterThreeFailures`: `NewDirWaiter` on a valid dir, then `Close()` the handles, then call `Wait(10*time.Millisecond)` four times; after the third failure `Degraded()` is true and the fourth call returns false after sleeping the timeout without touching the API.
4. `TestConfigFromEnvWiresWaiter`: ConfigFromEnv on a home with an existing state dir yields non-nil WaitEvent; on a home whose state dir does not exist yields nil WaitEvent.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/watch -v`
Expected: FAIL to build with `undefined: NewDirWaiter`.

- [ ] **Step 3: Implement**

Implementation notes for `notify_windows.go` (write real code preserving these decisions):
- One reusable 4KB buffer per waiter; the notification CONTENT is discarded, only the wake-up matters (the scan logic re-derives changes from signatures, so lost or coalesced events are harmless).
- The overlapped event is created once (`CreateEvent` via `syscall.CreateEvent`), reset before each issue.
- `Close` cancels in-flight IO with `syscall.CancelIo` before closing handles; Wait treats a canceled wait as a failure strike.
- Keep every raw handle private; the exported surface is exactly NewDirWaiter, Wait, Degraded, Close.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/watch -v`
Expected: PASS.

- [ ] **Step 5: E2E verification**

Repeat the Task 8 e2e run (watch fixture, append to `g1.status`) but WITHOUT setting `CFO_POLL=1`: with default 15s polling, the watcher must still close on the signal within ~5s of the append because the DirWaiter wakes it early (SignalGrace 1s via env).
Expected: signal close arrives fast; that speed difference is the notification path working.

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
- Consumes: `lock.Read`/`lock.HeldBy`/named-lock records, `home.Home`, `claudehook` env and emitters, `fsx`.
- Produces (all take stateDir first):
  - `supervise.Needed(stateDir string) (bool, string)`: true when any `*.meta` exists; the string is `"<N> task(s) in flight"`. NOT PORTED IN V1: procevent sources and x-watch checks.
  - `supervise.WatcherHealthy(stateDir string, grace time.Duration) bool`: the `.watch.lock` record names a live owner (reuse the lock package read + Alive via a small exported helper `lock.ReadNamed(dir, name string) (*Info, error)`) AND `.last-watcher-beat` mtime is younger than grace.
  - `supervise.AutoarmOwnsRecovery(stateDir string, grace, epochFresh time.Duration) bool`: WatcherHealthy, OR `.claude-autoarm.lock` names a live holder whose Session field is `autoarm`, OR the epoch ledger's outcome is one of `rewake`, `failed`, `failed-suppressed` with `updated_at` younger than epochFresh.
  - Budget over `state/.turnend-claude-blocks` (three lines `session=`, `count=`, `epoch=`), serialized by a create-exclusive `state/.turnend-claude-blocks.lockfile` (a FILE via O_CREATE|O_EXCL with bounded retry, not a directory): `supervise.ChargeBudget(stateDir, session string) (count int, err error)` increments (resetting to 1 when the recorded session differs) and `supervise.ResetBudget(stateDir string) error` removes the budget record plus both failure markers as one group under the lockfile. Upstream's same-epoch dedup within one Stop event is NOT PORTED IN V1 (Claude fires the guard once per Stop); the session-scoped counter is the load-bearing part.
  - Epoch ledger `state/.claude-autoarm-epoch`, single line `epoch=<n> owner_pid=<pid> outcome=<o> updated_at=<unix>`: `supervise.NextEpoch(stateDir string) (int, error)` (increments, writes outcome `arming`), `supervise.SetOutcome(stateDir string, epoch int, outcome string) error`, `supervise.ReadEpoch(stateDir string) (Epoch, error)` with `type Epoch struct { N int; OwnerPID int; Outcome string; UpdatedAt time.Time }`.
  - Failure markers: `supervise.MarkNotified/NotifiedOnce`, `supervise.MarkAlarm/AlarmFired` over `state/.claude-autoarm-failure-notified` and `-alarmed` (empty O_EXCL files; Mark is idempotent).
- `cfo hook turnend-guard` decision sequence (Claude-only; `stop_hook_active` is read but deliberately ignored, with the upstream 2026-07-21 incident comment):
  1. Transport fail-open; IsPrimary gate (exit 0 silent).
  2. `Needed` false: `ResetBudget` unless `NotifiedOnce` (a pending failure episode survives quiet turns); exit 0.
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

- [ ] **Step 1: Write the failing supervise tests**

Table-driven over temp state dirs:
1. Needed false on empty dir, true with `g1.meta` present, message says `1 task(s) in flight`.
2. WatcherHealthy false with no lock; false with live lock but stale beat (`os.Chtimes` to 10 minutes ago, grace 300s); true with live named lock (AcquireNamedOwner in-test) and fresh beat.
3. ChargeBudget counts 1,2,3 for one session; resets to 1 on session change; ResetBudget removes record and both markers.
4. NextEpoch increments across calls; SetOutcome updates outcome and timestamp; ReadEpoch round-trips.
5. AutoarmOwnsRecovery true via each of the three proofs independently; false with none.
6. Markers: NotifiedOnce false, MarkNotified, true; Mark is idempotent.

- [ ] **Step 2: Write the failing guard dispatcher tests**

`hook_test.go` cases driving `runHook("turnend-guard", ...)` with `CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS=1`:
1. Not primary: exit 0 silent.
2. Primary, no metas: exit 0, budget file removed if present.
3. Primary, meta present, healthy watcher (fixture named lock + fresh beat): exit 0.
4. Primary, meta present, nothing healthy: exit 2, stderr contains `TURN WOULD END BLIND` and `1 task(s) in flight`.
5. Fresh epoch outcome `rewake` (written moments before): exit 0.
6. Budget exhaustion path: pre-write budget count=3 for this session, create the notified marker, expect exit 0 with stdout JSON containing `GENUINELY DOWN`, and the alarmed marker now exists; a SECOND invocation blocks again with exit 2 (alarm is one-time).

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/supervise ./cmd/cfo -v`
Expected: FAIL to build.

- [ ] **Step 4: Implement**

`supervise.go` keeps every function under 40 lines; the budget lockfile helper (acquire with 10x50ms retry, defer-remove) is shared by ChargeBudget and ResetBudget.
`lock.ReadNamed` is a four-line addition to internal/lock (Read delegates to it with `.lock`).
The guard case in hook.go is a straight transcription of the six-step sequence; each step carries a one-line comment naming its upstream analogue.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/supervise ./internal/lock ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 6: E2E verification**

Build; in a fixture primary home with `state\g1.meta` present and no watcher, pipe `{"session_id":"s1"}` into `.\cfo.exe hook turnend-guard`: expect the blind-turn banner on stderr, exit 2.
Create `state\.last-watcher-beat` fresh plus a `.watch.lock` via a helper invocation is impractical from PowerShell; instead delete `g1.meta` and re-run: expect exit 0 silent.
Delete `.\cfo.exe`.

- [ ] **Step 7: Full-suite check and commit**

```powershell
git add internal/supervise internal/lock cmd/cfo
git commit -m "feat(hook): turnend guard with block budget and attended fail-open"
```

---

### Task 11: cfo hook stop-autoarm

**Files:**
- Modify: `cmd/cfo/hook.go` (stop-autoarm case plus its helpers)
- Modify: `cmd/cfo/hook_test.go`
- Modify: `internal/lock/lock.go` (add `ReleaseNamed(dir, name string) error`, calling-process keyed like Release)
- Modify: `internal/lock/lock_test.go` (one ReleaseNamed test)

**Interfaces:**
- Consumes: `proc.FindAncestor`, `lock.AcquireOwner`/`HeldBy`/`AcquireNamedOwner`/`ReleaseNamed`, `supervise.*`, `watch.Run`/`ConfigFromEnv`, `claudehook` transport.
- Produces: the `cfo hook stop-autoarm` behavior, the sole owner of watcher continuity. Claude fires it on every Stop with `asyncRewake: true` and an 8h timeout, undeduplicated; this process IS the watcher host: it runs `watch.Run` in-process and its eventual exit 2 stderr is what rewakes the idle agent.

Decision sequence:
1. Drain and discard stdin (decisions are state-based); IsPrimary gate, exit 0 silent.
2. Identity gate: `proc.FindAncestor(os.Getpid(), 16, "claude", "node")`; no harness ancestor found: exit 0 (comment: a manual shell invocation must never arm). Session custody: if `lock.HeldBy(state, ancestor.PID)` false, try `lock.AcquireOwner(state, ancestor.PID, payload session id or "")`; on ErrHeld (a different live owner) exit 0 inert.
3. Need gate: `supervise.Needed` false: exit 0.
4. Single-flight: `lock.AcquireNamedOwner(state, ".claude-autoarm.lock", os.Getpid(), "autoarm")`; ErrHeld: exit 0 immediately (another firing owns this Stop). `defer lock.ReleaseNamed(state, ".claude-autoarm.lock")`.
5. `epoch := supervise.NextEpoch(state)` (outcome `arming`).
6. Attempt loop, `AutoarmAttempts` bounded: `reason, err := watch.Run(watch.ConfigFromEnv(h))`.
   Lock-stolen return (empty reason, nil err): if `supervise.WatcherHealthy` then HEALTHY, break.
   Error return: strike, continue.
   Actionable reason (regexp `^(signal:|stale:|check:|heartbeat($|:))`): ACTIONABLE, break.
7. Outcomes, exactly one:
   - Need vanished (`Needed` false now): `SetOutcome clean`, exit 0.
   - HEALTHY: `ResetBudget`, `SetOutcome clean`, exit 0.
   - ACTIONABLE: `SetOutcome rewake`, stderr banner below, exit 2.
   - Failure, first in episode (`!NotifiedOnce`): `MarkNotified`, `SetOutcome failed`, stderr failure banner below, exit 2.
   - Failure, repeat (`NotifiedOnce`): `SetOutcome failed-suppressed`, exit 2 with EMPTY stderr (the silent retry keeps Stop-owned recovery alive without renotifying; upstream contract).

Rewake banner (verbatim, reason line inserted):

```
cfo watcher wake - one supervision event needs a handling turn now.
<reason>
Run cfo drain, handle what it presents, and acknowledge with the WAKE_ACK_REQUIRED command it prints. Do not run cfo watch manually after an ordinary wake.
```

Failure banner (attempts filled in):

```
cfo auto-arm FAILED after <N> attempt(s): the watcher could not hold this home.
Supervision is down and needs a repair turn: run cfo doctor, then verify the stop-autoarm hook registration in .claude/settings.json.
```

- [ ] **Step 1: Write the failing tests**

`hook_test.go` cases with tiny intervals (`CFO_POLL=1`, `CFO_SIGNAL_GRACE=1`, `CFO_HEARTBEAT=1`, `CFO_CLAUDE_AUTOARM_ATTEMPTS=1`); the harness-ancestor gate is satisfied because the test binary's ancestry contains no claude/node, so:
1. `TestAutoarmInertWithoutHarnessAncestor`: primary home with a meta file, expect exit 0 silent (this doubles as the manual-invocation safety test). To exercise deeper paths the ancestor gate is made testable: the hook consults `CFO_TEST_ANCESTOR_PID` when set (documented as test-only; refuse when the pid is not alive), letting tests pass `os.Getpid()`.
2. `TestAutoarmClean`: primary home, no metas, ancestor override set: exit 0, epoch outcome absent (need gate exits before epoch).
3. `TestAutoarmRewakeOnSignal`: meta present, status file appended by a goroutine after 300ms: exit 2, stderr contains `cfo watcher wake` and a `signal:` line, epoch outcome `rewake`, exactly one wake record pending.
4. `TestAutoarmHeartbeatIsActionable`: meta present, no changes, heartbeat 1s: exit 2, stderr contains `heartbeat`.
5. `TestAutoarmSingleFlight`: hold `.claude-autoarm.lock` for a live foreign pid with session `autoarm`; expect exit 0 fast.
6. `TestAutoarmFailureEpisode`: force `watch.Run` to fail by pre-holding `.watch.lock` for a live foreign process: first run exit 2 with `FAILED after 1 attempt(s)` and notified marker created, epoch `failed`; second run exit 2 with EMPTY stderr and epoch `failed-suppressed`.
7. `TestReleaseNamed` (lock package): acquire named, release, reacquire.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/cfo ./internal/lock -v`
Expected: FAIL (missing case and helpers).

- [ ] **Step 3: Implement**

The stop-autoarm case lives in hook.go as `hookStopAutoarm(h home.Home, payload claudehook.Payload, stdout, stderr io.Writer) int` with the seven-step sequence commented against upstream (`bin/fm-claude-stop-autoarm.sh` analogues).
`CFO_TEST_ANCESTOR_PID` is read only when set, validated alive via the lock package's tolerance-free liveness (a dead pid disables the override), and documented in a comment as a test seam, not a contract.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/cfo ./internal/lock -v`
Expected: PASS.

- [ ] **Step 5: E2E verification**

Build; fixture primary home with `state\g1.meta` and `state\g1.status`; `CFO_TEST_ANCESTOR_PID=$PID` (current PowerShell pid), tiny intervals; start `.\cfo.exe hook stop-autoarm` reading empty stdin as a background process with stderr redirected; append to `g1.status`; wait for exit.
Expected: exit code 2, stderr file contains `cfo watcher wake` and the `signal:` reason, `.\cfo.exe drain` (same CFO_HOME) shows the pending signal and the WAKE_ACK_REQUIRED line; then `cfo drain --ack-through <seq> --recovery-generation <gen>` empties it.
This is the full arm-wake-drain-ack cycle e2e, no Claude needed.
Delete `.\cfo.exe`.

- [ ] **Step 6: Full-suite check and commit**

```powershell
git add cmd/cfo internal/lock
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
- Modify: `internal/wake/wake.go` (export the drain renderer as `wake.Render(w io.Writer, records []Record, ep Episode)` and reuse it from cmd/cfo/drain.go)

**Interfaces:**
- Consumes: `lock.AcquireOwner`/`HeldBy`, `proc.FindAncestor`, `wake.Pending`/`Deduped`/`ReadEpisode`/`Render`, `state.ReadMeta`/`TailStatus`, `fsx.ReadLines`, `home`, `claudehook` env.
- Produces:
  - `digest.Compose(h home.Home, ownerPID int, session string, w io.Writer) error`: writes the full digest in this exact section order, each section beginning with a `== <NAME> ==` header line:
    1. `== SESSION LOCK ==`: AcquireOwner result; on ErrHeld print the read-only banner (`●`-ruled, names the holder pid and host, states that every mutating step below is skipped) and continue composing read-only.
    2. `== WAKE QUEUE ==`: `wake.Render` of the deduped pending records and episode; read-only, never acks.
    3. `== SUPERVISION OPERATING INSTRUCTIONS ==`: fixed text block stating: the Stop-owned auto-arm owns watcher continuity; never run `cfo watch` from the agent shell; wakes arrive as rewake turns; every drain presentation ends with a WAKE_ACK_REQUIRED command that must be run after handling; supervision is needed whenever tasks are in flight.
    4. `== READ-ONCE CONTRACT ==`: fixed text naming every file this digest prints in full (backlog, metas, status tails, projects, overlord, learnings) and forbidding re-reading them this turn.
    5. `== FLEET STATE ==`: backlog compact listing (first QueuedLimit unchecked `- [ ]` lines of `data\backlog.md` plus a `(+N more queued)` overflow line; done rows never listed; `ABSENT` when no file), then every `state\*.meta` (full key=value contents) with a StatusTail-line status tail, then orphan `.status` files (status without meta), then `(no goblins in flight)` when there are no metas.
    6. `== CONTEXT ==`: `data\projects.md`, `data\overlord.md`, `data\learnings.md`, each printed in full or as `<name>: ABSENT` / `<name>: (present, empty)`.
    7. `== NEXT STEP ==`: fixed two-line reminder to follow the supervision block; states the digest never arms anything itself.
    On success under a held lock, write `state\.session-start-complete` containing the owner pid (atomic).
  - Hook routing for `cfo hook session-start` by payload Source: `startup`, `new`, empty, or unrecognized: full Compose; `clear`, `compact`: full Compose only if `.session-start-complete` names the current lock owner AND `HeldBy` confirms it (a completed re-emit), else full Compose anyway (identical call; the marker only matters for the nudge path and future caching, keep the branch explicit with a comment); `resume`, `reload`, `fork`: print exactly `CFO: operational input may be waiting; run cfo drain if supervision was active.` and nothing else.
    ALWAYS exit 0: Compose errors are printed as digest text (`SESSION START DEGRADED: <err>`), never as a nonzero exit (a SessionStart exit 2 would block session init).
  - `cfo session-start` (no `hook` prefix): manual alias, full Compose, exit 0.
  - Owner resolution for the hook: `proc.FindAncestor(..., "claude", "node")`; when absent (manual invocation), fall back to `os.Getpid()` so the manual alias still works.

- [ ] **Step 1: Write the failing digest tests**

`digest_test.go` against fixture homes:
1. `TestComposeSectionOrder`: full fixture (backlog with 3 queued + 1 done, two metas, one orphan status, all three context files); output contains the seven `== ... ==` headers in order (assert via successive `strings.Index` comparisons).
2. `TestComposeBacklogCompact`: 25 queued rows with QueuedLimit 20 prints 20 plus `(+5 more queued)`; done rows absent from output.
3. `TestComposeAbsentContext`: missing overlord.md prints `overlord.md: ABSENT`.
4. `TestComposeReadOnlyOnHeldLock`: lock pre-held by a live foreign owner; output contains `READ-ONLY` and `.session-start-complete` is NOT written.
5. `TestComposeWritesCompleteMarker`: unheld lock; marker exists and contains the owner pid.
6. `TestComposeStatusTailCap`: a status log with 12 lines shows exactly the last 5 (StatusTail default).

Routing tests in `hook_test.go`:
7. Source `resume`: exit 0, stdout is exactly the nudge line.
8. Source `startup`: exit 0, stdout contains `== SESSION LOCK ==`.
9. Not primary: exit 0 silent.
10. `main_test.go` row: `session-start` alias dispatches (wantExit 0, wantStdout `== SESSION LOCK ==`, env fixture home).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/digest ./cmd/cfo -v`
Expected: FAIL to build.

- [ ] **Step 3: Implement**

`digest.go` is sequential section writers over one `io.Writer`; every file read is CRLF-tolerant via `fsx.ReadLines`; no subprocesses anywhere in the package (the 1s budget is met by construction; the upstream 120s watchdog is NOT PORTED IN V1 because no network or subprocess stage exists, comment says so).
`wake.Render` extraction: move the drain formatting verbatim from cmd/cfo/drain.go into the wake package and call it from both sites (no format change; drain tests keep passing untouched).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/digest ./internal/wake ./cmd/cfo -v`
Expected: PASS.

- [ ] **Step 5: E2E verification with the 1s budget**

Build; fixture primary home with two metas, statuses, backlog, and context files; run:

```powershell
$sw = [Diagnostics.Stopwatch]::StartNew()
'{"session_id":"s","source":"startup"}' | .\cfo.exe hook session-start | Out-File "$fix\digest.txt"
$sw.Stop(); "elapsed ms: $($sw.ElapsedMilliseconds)"
```
Expected: exit 0, digest.txt shows all seven sections in order, elapsed under 1000ms.
Then pipe `{"source":"resume"}`: exactly the one-line nudge.
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
- Modify: `docs/superpowers/specs/2026-08-12-windows-native-fork-design.md` is NOT touched (spec stays frozen); the wiring snippet lives in settings.json itself.

**Interfaces:**
- Consumes: everything Tasks 1-12 shipped.
- Produces: the live hook wiring and a repeatable proof that the whole family honors its contracts from a real binary.

- [ ] **Step 1: Write the failing e2e test**

`cmd/cfo/e2e_hooks_test.go`: a single `TestHookFamilyEndToEnd` that:
- Builds the real binary once into `t.TempDir()` with `exec.Command(goBin, "build", "-o", exe, "./...")` style invocation rooted at the repo (resolve `goBin` from `runtime.GOROOT()\bin\go.exe`; `t.Skip` if absent).
- Creates a fixture primary home (AGENTS.md, `state\`, git init) and a bare dev home (no `state\`).
- Table of six invocations against the PRIMARY home, each asserting exit code, stdout shape, stderr shape:
  1. `hook session-start` with `{"source":"startup"}`: exit 0, stdout has the seven section headers.
  2. `hook pretool-subagent` with tool_name Agent: exit 2, stderr envelope, empty stdout.
  3. `hook pretool-arm` with command `cfo watch &`: exit 2, code watcher-background.
  4. `hook pretool-cd` with command `cd C:\`: exit 2, code cwd-relocation.
  5. `hook turnend-guard` with one meta present: exit 2, `TURN WOULD END BLIND` (set CFO_CLAUDE_AUTOARM_SYNC_WAIT_MS=1).
  6. `hook stop-autoarm` with a meta, tiny intervals, CFO_TEST_ANCESTOR_PID set, and a status append goroutine: exit 2, `cfo watcher wake`.
- INERTNESS PROOF against the DEV home: all six invocations exit 0 with empty stdout AND stderr, and a recursive directory listing before and after is IDENTICAL (no file or directory created anywhere in the home). This assertion is the plan's inert-means-inert guarantee, mechanically enforced.

- [ ] **Step 2: Run it to verify current state**

Run: `go test ./cmd/cfo -run TestHookFamilyEndToEnd -v`
Expected: PASS already if Tasks 1-12 landed correctly; treat any failure as a real integration defect to fix now (this task has no new production code besides wiring).

- [ ] **Step 3: Wire .claude/settings.json**

Replace the file's `{}` with exactly:

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

The `[ -x ... ] || exit 0` guard makes every hook a silent no-op until a `cfo.exe` sits at the repo root (git-ignored), so dev sessions stay clean; `install.ps1` (Plan 5) owns wiring for installed homes.
The four events and six commands mirror upstream's registration one-to-one, `asyncRewake` and the 8h timeout preserved verbatim.

- [ ] **Step 4: Full verification battery**

Run: `go vet ./...` then `go test ./... -count=1` then the e2e test once more.
Expected: everything green.

- [ ] **Step 5: Commit**

```powershell
git add .claude/settings.json cmd/cfo/e2e_hooks_test.go
git commit -m "feat(hook): wire claude settings and whole-family e2e proof"
```

---

## Self-Review Notes

- Spec coverage: section 4's four events and six hook commands map to Tasks 6, 7, 10, 11, 12 with wiring in Task 13; section 2's watcher maps to Tasks 8 and 9 (fs notifications + timers, zero idle CPU via blocking waits, one-shot triage); the exit-code contract and stdout/stderr duality live in Task 2 and are enforced end-to-end in Task 13; the under-50ms and under-1s targets are asserted in Task 12's e2e step (digest) and by construction elsewhere (no hook spawns subprocesses except git rev-parse in the scope predicate).
- Sanctioned deviations from upstream, each marked in code comments: wake queue keeps Plan 1's JSON-lines storage while porting the kind whitelist, key-based dedup, detail-text conventions, and the recovery-generation episode marker (the documented drain contract is preserved; the byte format is not, because no bash reader survives the port); the cd-guard shares IsPrimary instead of its looser upstream predicate; the same-epoch budget dedup and the 120s digest watchdog are not ported (no multi-fire-per-Stop and no subprocess stages exist to need them); AFK, gate-agent refusal, network stage, check sweeps, staleness, procevent, and X-mode are v1 cuts per spec section 9.
- Type consistency: `home.Home` flows into every hook; `lock.Info` gains Session/OwnerPID in Task 4 before Tasks 8/10/11 consume named locks; `wake.Append`'s four-argument signature lands in Task 5 before Tasks 8 and 11 append records; `watch.Config`'s WaitEvent lands nil-able in Task 8 and is wired in Task 9.
- The plan's tests specify behavior contracts precisely but Tasks 2, 3, 5, 9-12 leave implementation bodies to the implementer within stated constraints; reviewers hold the contract tables above as the spec.

