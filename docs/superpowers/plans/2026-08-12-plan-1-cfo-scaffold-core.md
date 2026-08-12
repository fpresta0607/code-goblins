# code-goblins Plan 1: cfo.exe Scaffold and Core Packages

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the `cfo.exe` Go binary with its core packages (atomic file I/O, meta files, status logs, session lock, wake queue, doctor) and Windows CI, replacing the inherited bash CI.

**Architecture:** One pure-Go module at the repo root; `cmd/cfo` dispatches subcommands to small `internal/` packages that own one concern each. All file formats keep upstream First Mate's on-disk shapes (`key=value` meta files, append-only status logs, a durable sequenced wake queue under `state/`). The MSYS symlink lock is replaced by a create-exclusive lock file with PID-plus-creation-time identity.

**Tech Stack:** Go (stable, currently 1.25), Go standard library only (zero third-party deps in this plan), GitHub Actions `windows-latest`.

This is Plan 1 of a series.
Later plans build on these packages: Plan 2 Claude hooks and watcher, Plan 3 fleet operations (treehouse worktrees, Herdr backend, spawn/send/peek), Plan 4 delivery (PR flows, backlog, tasks-axi), Plan 5 instruction-layer rebrand, install.ps1, and releases.

## Global Constraints

- Module path: `github.com/fpresta0607/code-goblins`; binary name `cfo` (builds `cfo.exe` on Windows).
- Pure Go, no cgo, and zero third-party Go dependencies in this plan.
- Windows is the only target platform; platform-specific files use the `_windows.go` suffix.
- No symlinks anywhere, in code or tests.
- Every state-file write is atomic: temp file in the same directory, then rename, with bounded retry on sharing violations.
- All line-oriented parsers accept CRLF and LF equally.
- State lives under a home directory's `state/` subdirectory; functions take the state directory as their first parameter (no globals).
- Naming in all output and docs: the human is the Supreme Overlord, the primary agent is the Chief Fuckaround Officer (CFO), workers are Code Goblins.
- Commits follow `<type>(<scope>): <subject>` (imperative, lowercase, no period, under 72 chars); commit after every green test cycle.
- Markdown files put each full sentence on its own physical line; no em dash characters anywhere.
- Working directory for all commands: `C:\dev\code-goblins`.
- Do not modify anything under `C:\dev\firstmate` (the upstream reference clone).
- The shell is PowerShell unless a step says otherwise; `go` commands are shell-agnostic.

## File Structure

```
go.mod                          module definition
cmd/cfo/main.go                 entry point: subcommand dispatch, version, usage
cmd/cfo/main_test.go            dispatch table tests
internal/fsx/fsx.go             atomic writes, CRLF-tolerant line reads
internal/fsx/fsx_test.go
internal/state/meta.go          key=value task metadata files (state/<id>.meta)
internal/state/meta_test.go
internal/state/status.go        append-only status logs (state/<id>.status)
internal/state/status_test.go
internal/lock/lock.go           session lock acquire/read/release (state/.lock)
internal/lock/proc_windows.go   PID liveness + creation time via Win32
internal/lock/lock_test.go
internal/wake/wake.go           durable sequenced wake queue (state/.wake-queue)
internal/wake/wake_test.go
internal/doctor/doctor.go       required-tool checks with install hints
internal/doctor/doctor_test.go
.github/workflows/go.yml        vet + test + build on windows-latest
```

Deleted in this plan: `.github/workflows/ci.yml`, `.github/workflows/no-mistakes-required.yml`, `.github/workflows/windows-herdr-spike.yml` (they exercise the bash layer this fork replaces).
The bash `bin/` and `tests/` trees stay in place; later plans delete each script as its Go replacement lands.

---

### Task 1: Go toolchain, module, and cfo dispatch skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/cfo/main.go`
- Test: `cmd/cfo/main_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `run(args []string, stdout, stderr io.Writer) int` in `package main`, the dispatch point every later CLI task extends; `var version = "dev"` overridable via `-ldflags "-X main.version=..."`.

- [ ] **Step 1: Install Go (machine has none)**

Run: `winget install --id GoLang.Go --silent --accept-package-agreements --accept-source-agreements`
Then open a fresh shell (winget edits PATH) and verify: `go version`
Expected: `go version go1.2x.x windows/amd64` (any current stable).

- [ ] **Step 2: Create the module**

Run in `C:\dev\code-goblins`: `go mod init github.com/fpresta0607/code-goblins`
Expected: `go.mod` created containing the module path and a `go 1.2x` line.

- [ ] **Step 3: Write the failing dispatch test**

Create `cmd/cfo/main_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{name: "no args prints usage", args: nil, wantExit: 2, wantStderr: "usage: cfo"},
		{name: "unknown command", args: []string{"nonsense"}, wantExit: 2, wantStderr: `unknown command "nonsense"`},
		{name: "version", args: []string{"version"}, wantExit: 0, wantStdout: "cfo dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			got := run(tt.args, &stdout, &stderr)
			if got != tt.wantExit {
				t.Errorf("exit = %d, want %d", got, tt.wantExit)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./cmd/cfo -v`
Expected: FAIL to build with `undefined: run`.

- [ ] **Step 5: Write the minimal implementation**

Create `cmd/cfo/main.go`:

```go
// Command cfo is the Chief Fuckaround Officer's tool belt: the compiled,
// Windows-native replacement for upstream First Mate's bash script layer.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/cfo
var version = "dev"

const usage = `usage: cfo <command> [args]

commands:
  version   print the cfo version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "cfo %s\n", version)
		return 0
	default:
		fmt.Fprintf(stderr, "cfo: unknown command %q\n%s", args[0], usage)
		return 2
	}
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./cmd/cfo -v`
Expected: PASS (all three subtests).

- [ ] **Step 7: Build the binary as a smoke check**

Run: `go build ./cmd/cfo` then `.\cfo.exe version`
Expected: prints `cfo dev`.
Then delete the local build artifact and ignore future ones:

```powershell
Remove-Item cfo.exe
Add-Content .gitignore "/cfo.exe"
```

- [ ] **Step 8: Commit**

```powershell
git add go.mod cmd/cfo/main.go cmd/cfo/main_test.go .gitignore
git commit -m "feat(cfo): go module and subcommand dispatch skeleton"
```

---

### Task 2: internal/fsx, atomic writes and CRLF-tolerant reads

**Files:**
- Create: `internal/fsx/fsx.go`
- Test: `internal/fsx/fsx_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `fsx.AtomicWriteFile(path string, data []byte) error` and `fsx.ReadLines(path string) ([]string, error)`; every later package uses these for state I/O.
  `ReadLines` propagates `os.ErrNotExist` for missing files; callers decide what absence means.

- [ ] **Step 1: Write the failing tests**

Create `internal/fsx/fsx_test.go`:

```go
package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := AtomicWriteFile(path, []byte("hello\n")); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content = %q, want %q", got, "hello\n")
	}
}

func TestAtomicWriteFileOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFile(path, []byte("new")); err != nil {
		t.Fatalf("AtomicWriteFile over existing: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

func TestAtomicWriteFileLeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := AtomicWriteFile(filepath.Join(dir, "out.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (no leftover temp files)", len(entries))
	}
}

func TestReadLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{name: "lf", content: "a\nb\n", want: []string{"a", "b"}},
		{name: "crlf", content: "a\r\nb\r\n", want: []string{"a", "b"}},
		{name: "mixed", content: "a\r\nb\nc", want: []string{"a", "b", "c"}},
		{name: "empty file", content: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.txt")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := ReadLines(path)
			if err != nil {
				t.Fatalf("ReadLines: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("lines = %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReadLinesMissingFile(t *testing.T) {
	_, err := ReadLines(filepath.Join(t.TempDir(), "absent.txt"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/fsx -v`
Expected: FAIL to build with `undefined: AtomicWriteFile`.

- [ ] **Step 3: Write the implementation**

Create `internal/fsx/fsx.go`:

```go
// Package fsx holds the Windows-safe file primitives every state package
// builds on: atomic replace-on-rename writes and CRLF-tolerant line reads.
package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AtomicWriteFile replaces path with data by writing a temp file in the same
// directory and renaming it over path. The rename retries briefly because
// antivirus and indexer scans on Windows hold transient sharing locks.
func AtomicWriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cfo-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmpName)
		return err
	}
	var renameErr error
	for attempt := 0; attempt < 10; attempt++ {
		if renameErr = os.Rename(tmpName, path); renameErr == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	os.Remove(tmpName)
	return renameErr
}

// ReadLines returns the file's lines, treating CRLF and LF endings equally.
// A missing file returns an error satisfying errors.Is(err, os.ErrNotExist).
func ReadLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/fsx -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```powershell
git add internal/fsx
git commit -m "feat(fsx): atomic writes and crlf-tolerant line reads"
```

---

### Task 3: internal/state, key=value meta files

**Files:**
- Create: `internal/state/meta.go`
- Test: `internal/state/meta_test.go`

**Interfaces:**
- Consumes: `fsx.ReadLines`, `fsx.AtomicWriteFile`.
- Produces: `state.ReadMeta(path string) (map[string]string, error)` and `state.WriteMeta(path string, kv map[string]string) error`.
  Format matches upstream First Mate exactly: one `key=value` per line, later duplicate keys win, non-`key=value` lines are inert (upstream readers grep `^key=` only).

- [ ] **Step 1: Write the failing tests**

Create `internal/state/meta_test.go`:

```go
package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMetaLastKeyWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g1.meta")
	content := "worktree=C:\\wt\\g1\nkind=ship\nworktree=C:\\wt\\g1-respawn\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	kv, err := ReadMeta(path)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if kv["worktree"] != `C:\wt\g1-respawn` {
		t.Errorf("worktree = %q, want the later value", kv["worktree"])
	}
	if kv["kind"] != "ship" {
		t.Errorf("kind = %q, want %q", kv["kind"], "ship")
	}
}

func TestReadMetaIgnoresNonPairLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g1.meta")
	if err := os.WriteFile(path, []byte("# comment\n\nkind=scout\n=orphan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	kv, err := ReadMeta(path)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if len(kv) != 1 || kv["kind"] != "scout" {
		t.Errorf("kv = %v, want only kind=scout", kv)
	}
}

func TestReadMetaValueMayContainEquals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g1.meta")
	if err := os.WriteFile(path, []byte("endpoint=session=s1;pane=p2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	kv, err := ReadMeta(path)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if kv["endpoint"] != "session=s1;pane=p2" {
		t.Errorf("endpoint = %q, split must happen on the first '=' only", kv["endpoint"])
	}
}

func TestReadMetaMissingFile(t *testing.T) {
	_, err := ReadMeta(filepath.Join(t.TempDir(), "absent.meta"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestWriteMetaRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g2.meta")
	in := map[string]string{"kind": "ship", "harness": "claude", "worktree": `C:\wt\g2`}
	if err := WriteMeta(path, in); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	out, err := ReadMeta(path)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round trip lost keys: %v", out)
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("%s = %q, want %q", k, out[k], v)
		}
	}
}

func TestWriteMetaDeterministicOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g3.meta")
	if err := WriteMeta(path, map[string]string{"b": "2", "a": "1"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "a=1\nb=2\n" {
		t.Errorf("file = %q, want sorted keys %q", data, "a=1\nb=2\n")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/state -v`
Expected: FAIL to build with `undefined: ReadMeta`.

- [ ] **Step 3: Write the implementation**

Create `internal/state/meta.go`:

```go
// Package state reads and writes the on-disk task state First Mate defined:
// key=value meta files and append-only status logs under a home's state dir.
package state

import (
	"maps"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ReadMeta parses a state/<id>.meta file: one key=value pair per line, the
// last occurrence of a key wins, and lines without a key before '=' are inert.
// The shape matches upstream First Mate's meta readers (grep "^key=" | tail -1).
func ReadMeta(path string) (map[string]string, error) {
	lines, err := fsx.ReadLines(path)
	if err != nil {
		return nil, err
	}
	kv := make(map[string]string)
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			continue
		}
		kv[key] = value
	}
	return kv, nil
}

// WriteMeta atomically writes kv as sorted key=value lines.
func WriteMeta(path string, kv map[string]string) error {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(kv)) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(kv[k])
		b.WriteByte('\n')
	}
	return fsx.AtomicWriteFile(path, []byte(b.String()))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/state -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```powershell
git add internal/state
git commit -m "feat(state): key=value meta files with upstream shape"
```

---

### Task 4: internal/state, append-only status logs

**Files:**
- Modify: create `internal/state/status.go` (same package as Task 3)
- Test: `internal/state/status_test.go`

**Interfaces:**
- Consumes: `fsx.ReadLines`.
- Produces: `state.AppendStatus(dir, id, line string) error` and `state.TailStatus(dir, id string, n int) ([]string, error)`.
  Lines are appended raw (no added timestamp); the status-line grammar is owned by the classify port in a later plan.
  `TailStatus` on a missing log returns `(nil, nil)`: "no status yet" is a real fleet state, not an error.

- [ ] **Step 1: Write the failing tests**

Create `internal/state/status_test.go`:

```go
package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendStatusCreatesAndAppends(t *testing.T) {
	dir := t.TempDir()
	if err := AppendStatus(dir, "g1", "spawned"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	if err := AppendStatus(dir, "g1", "working"); err != nil {
		t.Fatalf("AppendStatus second: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "g1.status"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "spawned\nworking\n" {
		t.Errorf("log = %q, want %q", data, "spawned\nworking\n")
	}
}

func TestTailStatusBounds(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"one", "two", "three", "four"} {
		if err := AppendStatus(dir, "g1", line); err != nil {
			t.Fatal(err)
		}
	}
	got, err := TailStatus(dir, "g1", 2)
	if err != nil {
		t.Fatalf("TailStatus: %v", err)
	}
	if len(got) != 2 || got[0] != "three" || got[1] != "four" {
		t.Errorf("tail = %q, want [three four]", got)
	}
}

func TestTailStatusFewerLinesThanAsked(t *testing.T) {
	dir := t.TempDir()
	if err := AppendStatus(dir, "g1", "only"); err != nil {
		t.Fatal(err)
	}
	got, err := TailStatus(dir, "g1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "only" {
		t.Errorf("tail = %q, want [only]", got)
	}
}

func TestTailStatusMissingLogMeansNoStatusYet(t *testing.T) {
	got, err := TailStatus(t.TempDir(), "ghost", 5)
	if err != nil {
		t.Fatalf("missing log must not error, got %v", err)
	}
	if got != nil {
		t.Errorf("tail = %v, want nil", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/state -v`
Expected: FAIL to build with `undefined: AppendStatus`.

- [ ] **Step 3: Write the implementation**

Create `internal/state/status.go`:

```go
package state

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// AppendStatus appends one raw line to state/<id>.status, creating the log on
// first use. Lines carry their own grammar; this layer adds nothing.
func AppendStatus(dir, id, line string) error {
	f, err := os.OpenFile(filepath.Join(dir, id+".status"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(line + "\n")
	return errors.Join(werr, f.Close())
}

// TailStatus returns the last n lines of state/<id>.status. A missing log
// returns (nil, nil): "no status yet" is a real fleet state, not an error.
// ponytail: whole-file read; switch to a reverse block scan if logs outgrow
// the line caps a later plan ports.
func TailStatus(dir, id string, n int) ([]string, error) {
	lines, err := fsx.ReadLines(filepath.Join(dir, id+".status"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/state -v`
Expected: PASS (meta tests from Task 3 plus all status subtests).

- [ ] **Step 5: Commit**

```powershell
git add internal/state/status.go internal/state/status_test.go
git commit -m "feat(state): append-only status logs with bounded tail"
```

---

### Task 5: internal/lock, session lock with PID-plus-creation-time identity

**Files:**
- Create: `internal/lock/lock.go`
- Create: `internal/lock/proc_windows.go`
- Test: `internal/lock/lock_test.go`

**Interfaces:**
- Consumes: nothing from other tasks (uses `os` and `encoding/json` directly; the lock file must be create-exclusive, which `fsx.AtomicWriteFile` cannot express).
- Produces:
  - `type lock.Info struct { PID int; Start time.Time; Hostname string; Acquired time.Time }` (all fields JSON-tagged lowercase).
  - `lock.Acquire(dir string) (*Info, error)`; returns `lock.ErrHeld` (wrapped) when a live process holds it.
  - `lock.Read(dir string) (*Info, error)`.
  - `(*Info).Alive() bool`.
  - `lock.Release(dir string) error`; only the acquiring process identity may release.
  - `processStart(pid int) (time.Time, bool)` stays unexported; `Alive` is the public surface.

This replaces upstream's MSYS symlink lock and harness-ancestry walk, the machinery behind "cannot locate harness process in ancestry" read-only sessions.
Identity is PID plus process creation time, so a recycled PID cannot impersonate a dead holder.

- [ ] **Step 1: Write the failing tests**

Create `internal/lock/lock_test.go`:

```go
package lock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	info, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
	}
	if info.Start.IsZero() {
		t.Error("Start is zero, want the process creation time")
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); err != nil {
		t.Errorf("lock file missing: %v", err)
	}
}

func TestSecondAcquireFailsWhileHolderLives(t *testing.T) {
	dir := t.TempDir()
	if _, err := Acquire(dir); err != nil {
		t.Fatal(err)
	}
	_, err := Acquire(dir)
	if !errors.Is(err, ErrHeld) {
		t.Errorf("err = %v, want ErrHeld", err)
	}
}

func TestAcquireStealsFromDeadHolder(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.ProcessState.Pid()
	stale := &Info{PID: deadPID, Start: time.Now().Add(-time.Hour), Hostname: "host", Acquired: time.Now().Add(-time.Hour)}
	if err := writeInfo(filepath.Join(dir, ".lock"), stale); err != nil {
		t.Fatal(err)
	}
	info, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire over dead holder: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want current process %d", info.PID, os.Getpid())
	}
}

func TestReleaseThenReacquire(t *testing.T) {
	dir := t.TempDir()
	if _, err := Acquire(dir); err != nil {
		t.Fatal(err)
	}
	if err := Release(dir); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := Acquire(dir); err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
}

func TestAliveForSelfAndDead(t *testing.T) {
	self := selfInfo()
	if !self.Alive() {
		t.Error("current process must be alive")
	}
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	dead := &Info{PID: cmd.ProcessState.Pid(), Start: time.Now().Add(-time.Hour)}
	if dead.Alive() {
		t.Error("exited process must not be alive (pid gone or start mismatch)")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/lock -v`
Expected: FAIL to build with `undefined: Acquire` (and the other names).

- [ ] **Step 3: Write the Windows process identity file**

Create `internal/lock/proc_windows.go`:

```go
package lock

import (
	"syscall"
	"time"
)

// Windows access right allowing process metadata queries without wider rights.
const processQueryLimitedInformation = 0x1000

// processStart returns pid's creation time, or ok=false when the process does
// not exist or cannot be queried (which, for lock custody, means "not alive").
func processStart(pid int) (time.Time, bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return time.Time{}, false
	}
	defer syscall.CloseHandle(h)
	// Exit time is NOT checked: MSDN documents it as undefined while the
	// process runs. An exited process fails OpenProcess once its handles
	// close, and a recycled PID is caught by the creation-time comparison.
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, creation.Nanoseconds()).UTC(), true
}
```

- [ ] **Step 4: Write the lock implementation**

Create `internal/lock/lock.go`:

```go
// Package lock owns the home's session lock: one primary session may mutate
// fleet state. Identity is PID plus process creation time, replacing upstream's
// MSYS symlink lock and harness-ancestry walk.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrHeld reports that a live process holds the session lock.
var ErrHeld = errors.New("session lock held by a live process")

// Info identifies a lock holder.
type Info struct {
	PID      int       `json:"pid"`
	Start    time.Time `json:"start"`
	Hostname string    `json:"hostname"`
	Acquired time.Time `json:"acquired"`
}

// Alive reports whether the holder's process still runs. FILETIME rounding
// means creation times can differ by a tick, so match within one second.
func (i *Info) Alive() bool {
	start, ok := processStart(i.PID)
	if !ok {
		return false
	}
	diff := start.Sub(i.Start)
	return diff > -time.Second && diff < time.Second
}

func selfInfo() *Info {
	start, _ := processStart(os.Getpid())
	hostname, _ := os.Hostname()
	return &Info{PID: os.Getpid(), Start: start, Hostname: hostname, Acquired: time.Now().UTC()}
}

func writeInfo(path string, info *Info) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	return errors.Join(werr, f.Close())
}

// Acquire takes dir/.lock for the current process. A holder that is dead
// (PID gone, or PID recycled with a different creation time) is stolen.
func Acquire(dir string) (*Info, error) {
	path := filepath.Join(dir, ".lock")
	self := selfInfo()
	for attempt := 0; attempt < 3; attempt++ {
		err := writeInfo(path, self)
		if err == nil {
			return self, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		holder, herr := Read(dir)
		if herr == nil && holder.Alive() {
			return nil, fmt.Errorf("%w: pid %d on %s since %s",
				ErrHeld, holder.PID, holder.Hostname, holder.Acquired.Format(time.RFC3339))
		}
		// Dead or unreadable holder: clear the stale file and retry the
		// exclusive create. Losing that race to a concurrent acquirer is
		// correct behavior, not an error.
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return nil, rerr
		}
	}
	return nil, errors.New("lock: lost the create race three times")
}

// Read returns the current holder recorded in dir/.lock.
func Read(dir string) (*Info, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".lock"))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("lock: unreadable holder record: %w", err)
	}
	return &info, nil
}

// Release removes the lock when the current process identity holds it.
func Release(dir string) error {
	holder, err := Read(dir)
	if err != nil {
		return err
	}
	self := selfInfo()
	if holder.PID != self.PID || !holder.Alive() {
		return fmt.Errorf("lock: held by pid %d, not this process", holder.PID)
	}
	return os.Remove(filepath.Join(dir, ".lock"))
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/lock -v`
Expected: PASS (all five tests).

- [ ] **Step 6: Commit**

```powershell
git add internal/lock
git commit -m "feat(lock): session lock with pid-plus-creation-time identity"
```

---

### Task 6: internal/wake, durable sequenced wake queue

**Files:**
- Create: `internal/wake/wake.go`
- Test: `internal/wake/wake_test.go`

**Interfaces:**
- Consumes: `fsx.ReadLines`, `fsx.AtomicWriteFile`.
- Produces:
  - `type wake.Record struct { Seq int; Time time.Time; Kind string; Detail string }` (JSON tags `seq`, `time`, `kind`, `detail`).
  - `wake.Append(dir, kind, detail string) (Record, error)`.
  - `wake.Pending(dir string) ([]Record, error)`.
  - `wake.AckThrough(dir string, seq int) error`.
  The file is `state/.wake-queue`, one JSON record per line; Seq starts at 1, grows monotonically, and is never reused, preserving upstream's drain-then-`--ack-through` contract.

- [ ] **Step 1: Write the failing tests**

Create `internal/wake/wake_test.go`:

```go
package wake

import (
	"testing"
)

func TestAppendAssignsSequence(t *testing.T) {
	dir := t.TempDir()
	for want := 1; want <= 3; want++ {
		rec, err := Append(dir, "signal", "goblin g1 finished")
		if err != nil {
			t.Fatalf("Append %d: %v", want, err)
		}
		if rec.Seq != want {
			t.Errorf("Seq = %d, want %d", rec.Seq, want)
		}
	}
}

func TestPendingReturnsAllInOrder(t *testing.T) {
	dir := t.TempDir()
	kinds := []string{"signal", "stale", "check"}
	for _, k := range kinds {
		if _, err := Append(dir, k, "detail of "+k); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, k := range kinds {
		if got[i].Kind != k || got[i].Seq != i+1 {
			t.Errorf("record %d = %+v, want kind %s seq %d", i, got[i], k, i+1)
		}
	}
}

func TestAckThroughDropsHandledKeepsRest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := Append(dir, "signal", "d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := AckThrough(dir, 2); err != nil {
		t.Fatalf("AckThrough: %v", err)
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != 3 {
		t.Errorf("pending = %+v, want only seq 3", got)
	}
}

func TestSequenceNeverReusedAfterAck(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := Append(dir, "signal", "d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := AckThrough(dir, 3); err != nil {
		t.Fatal(err)
	}
	rec, err := Append(dir, "signal", "after ack")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 4 {
		t.Errorf("Seq = %d, want 4 (sequences are never reused)", rec.Seq)
	}
}

func TestAckThroughIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Append(dir, "signal", "d"); err != nil {
		t.Fatal(err)
	}
	if err := AckThrough(dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := AckThrough(dir, 1); err != nil {
		t.Fatalf("second identical ack must succeed, got %v", err)
	}
	got, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("pending = %+v, want empty", got)
	}
}

func TestPendingEmptyWhenNoQueueFile(t *testing.T) {
	got, err := Pending(t.TempDir())
	if err != nil {
		t.Fatalf("missing queue must mean empty, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("pending = %+v, want empty", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/wake -v`
Expected: FAIL to build with `undefined: Append`.

- [ ] **Step 3: Write the implementation**

Create `internal/wake/wake.go`:

```go
// Package wake owns the durable wake queue: sequenced records a watcher
// appends and a drain turn acknowledges, surviving restarts in between.
package wake

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const queueFile = ".wake-queue"

// Record is one durable wake. Seq starts at 1 and is never reused; the ack
// floor only ever moves forward, matching upstream's --ack-through contract.
type Record struct {
	Seq    int       `json:"seq"`
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}

// ackFile persists the highest acknowledged sequence so acked sequences stay
// retired even once the queue file empties.
const ackFile = ".wake-ack"

func readAckFloor(dir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(dir, ackFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var floor int
	if _, err := fmt.Sscanf(string(data), "%d", &floor); err != nil {
		return 0, fmt.Errorf("wake: unreadable ack floor: %w", err)
	}
	return floor, nil
}

func readAll(dir string) ([]Record, error) {
	lines, err := fsx.ReadLines(filepath.Join(dir, queueFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(lines))
	for _, line := range lines {
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("wake: corrupt queue line %q: %w", line, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

// Append adds one record and returns it with its assigned sequence.
// ponytail: single-writer sequencing (the watcher); add a lock file here if a
// second emitter ever appends concurrently.
func Append(dir, kind, detail string) (Record, error) {
	records, err := readAll(dir)
	if err != nil {
		return Record{}, err
	}
	floor, err := readAckFloor(dir)
	if err != nil {
		return Record{}, err
	}
	next := floor + 1
	if n := len(records); n > 0 {
		next = records[n-1].Seq + 1
	}
	rec := Record{Seq: next, Time: time.Now().UTC(), Kind: kind, Detail: detail}
	line, err := json.Marshal(rec)
	if err != nil {
		return Record{}, err
	}
	f, err := os.OpenFile(filepath.Join(dir, queueFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Record{}, err
	}
	_, werr := f.Write(append(line, '\n'))
	if err := errors.Join(werr, f.Close()); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Pending returns every unacknowledged record in sequence order.
func Pending(dir string) ([]Record, error) {
	return readAll(dir)
}

// AckThrough retires every record with Seq <= seq and advances the durable
// ack floor. Acking an already-empty or already-acked range is a no-op.
func AckThrough(dir string, seq int) error {
	records, err := readAll(dir)
	if err != nil {
		return err
	}
	kept := records[:0]
	for _, rec := range records {
		if rec.Seq > seq {
			kept = append(kept, rec)
		}
	}
	var b []byte
	for _, rec := range kept {
		line, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	if err := fsx.AtomicWriteFile(filepath.Join(dir, queueFile), b); err != nil {
		return err
	}
	floor, err := readAckFloor(dir)
	if err != nil {
		return err
	}
	if seq > floor {
		floor = seq
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, ackFile), []byte(fmt.Sprintf("%d\n", floor)))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/wake -v`
Expected: PASS (all six tests, including seq 4 after acking 1-3).

- [ ] **Step 5: Commit**

```powershell
git add internal/wake
git commit -m "feat(wake): durable sequenced wake queue with ack floor"
```

---

### Task 7: internal/doctor and the cfo doctor command

**Files:**
- Create: `internal/doctor/doctor.go`
- Test: `internal/doctor/doctor_test.go`
- Modify: `cmd/cfo/main.go` (add the `doctor` case to `run` and the usage text)
- Modify: `cmd/cfo/main_test.go` (add a dispatch test)

**Interfaces:**
- Consumes: nothing from other packages.
- Produces:
  - `type doctor.Check struct { Name, Version, Err, Hint string }`.
  - `doctor.Run() []Check` (checks, in order: git, gh, claude, herdr, treehouse).
  - `doctor.Healthy(checks []Check) bool` (true when every check has an empty `Err`).
  - `cfo doctor` exit 0 when healthy, 1 otherwise.
  treehouse is a presence-only check (its `--version` behavior on Windows is verified in Plan 3); the other four run `<tool> --version`.

- [ ] **Step 1: Write the failing package tests**

Create `internal/doctor/doctor_test.go`.
The test builds a fake toolbox on PATH: real `.bat` files standing in for each tool, which exercises the same `exec.LookPath` plus `--version` path production uses.

```go
package doctor

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fakeTool writes a .bat file that prints out and exits with code.
func fakeTool(t *testing.T, dir, name, out string, code int) {
	t.Helper()
	script := "@echo off\r\necho " + out + "\r\nexit /b " + strconv.Itoa(code) + "\r\n"
	if err := os.WriteFile(filepath.Join(dir, name+".bat"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunAllToolsPresent(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "claude", "herdr", "treehouse"} {
		fakeTool(t, dir, name, name+" version 1.0.0", 0)
	}
	t.Setenv("PATH", dir)
	checks := Run()
	if len(checks) != 5 {
		t.Fatalf("len = %d, want 5", len(checks))
	}
	if !Healthy(checks) {
		t.Errorf("Healthy = false with all tools present: %+v", checks)
	}
	if checks[0].Name != "git" || checks[0].Version != "git version 1.0.0" {
		t.Errorf("git check = %+v, want captured version line", checks[0])
	}
}

func TestRunMissingToolCarriesHint(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "claude", "herdr"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	t.Setenv("PATH", dir) // no treehouse
	checks := Run()
	if Healthy(checks) {
		t.Error("Healthy = true with treehouse missing")
	}
	last := checks[4]
	if last.Name != "treehouse" || last.Err == "" || last.Hint == "" {
		t.Errorf("treehouse check = %+v, want Err and Hint set", last)
	}
}

func TestRunBrokenToolReportsFailure(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"gh", "claude", "herdr", "treehouse"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	fakeTool(t, dir, "git", "boom", 1)
	t.Setenv("PATH", dir)
	checks := Run()
	if Healthy(checks) {
		t.Error("Healthy = true with git --version failing")
	}
	if checks[0].Name != "git" || checks[0].Err == "" {
		t.Errorf("git check = %+v, want Err set", checks[0])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/doctor -v`
Expected: FAIL to build with `undefined: Run`.

- [ ] **Step 3: Write the implementation**

Create `internal/doctor/doctor.go`:

```go
// Package doctor verifies the tools cfo shells out to, with install hints.
package doctor

import (
	"os/exec"
	"strings"
)

// Check is one tool's verdict. Err empty means usable.
type Check struct {
	Name    string
	Version string
	Err     string
	Hint    string
}

var tools = []struct {
	name         string
	presenceOnly bool
	hint         string
}{
	{name: "git", hint: "winget install Git.Git"},
	{name: "gh", hint: "winget install GitHub.cli, then gh auth login"},
	{name: "claude", hint: "npm install -g @anthropic-ai/claude-code"},
	{name: "herdr", hint: "irm https://herdr.dev/install.ps1 | iex"},
	// Presence-only until Plan 3 verifies treehouse --version on Windows.
	{name: "treehouse", presenceOnly: true, hint: "see github.com/kunchenguid/treehouse"},
}

// Run checks every required tool in a fixed order.
func Run() []Check {
	checks := make([]Check, 0, len(tools))
	for _, tool := range tools {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			checks = append(checks, Check{Name: tool.name, Err: "not found on PATH", Hint: tool.hint})
			continue
		}
		if tool.presenceOnly {
			checks = append(checks, Check{Name: tool.name, Version: "present at " + path})
			continue
		}
		out, err := exec.Command(path, "--version").Output()
		if err != nil {
			checks = append(checks, Check{Name: tool.name, Err: tool.name + " --version failed", Hint: tool.hint})
			continue
		}
		version, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		checks = append(checks, Check{Name: tool.name, Version: strings.TrimSpace(version)})
	}
	return checks
}

// Healthy reports whether every check passed.
func Healthy(checks []Check) bool {
	for _, c := range checks {
		if c.Err != "" {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run the package tests to verify they pass**

Run: `go test ./internal/doctor -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Write the failing CLI dispatch test**

Add to the `tests` slice in `TestRun` in `cmd/cfo/main_test.go` (the fake PATH from the doctor package tests is not repeated here; this asserts dispatch and output shape only, against whatever the real machine has):

```go
		{name: "doctor runs and reports each tool", args: []string{"doctor"}, wantExit: -1, wantStdout: "git"},
```

And add this handling right after the `got != tt.wantExit` comparison, replacing it:

```go
			if tt.wantExit != -1 && got != tt.wantExit {
				t.Errorf("exit = %d, want %d", got, tt.wantExit)
			}
			if tt.wantExit == -1 && got != 0 && got != 1 {
				t.Errorf("exit = %d, want 0 or 1 (doctor's health verdict)", got)
			}
```

- [ ] **Step 6: Run the CLI test to verify it fails**

Run: `go test ./cmd/cfo -v`
Expected: FAIL: the `doctor` subtest hits the `unknown command` branch (exit 2).

- [ ] **Step 7: Wire doctor into the CLI**

In `cmd/cfo/main.go`, add the import `"github.com/fpresta0607/code-goblins/internal/doctor"` and extend the switch in `run`:

```go
	case "doctor":
		checks := doctor.Run()
		for _, c := range checks {
			if c.Err != "" {
				fmt.Fprintf(stdout, "MISSING  %-10s %s (install: %s)\n", c.Name, c.Err, c.Hint)
			} else {
				fmt.Fprintf(stdout, "ok       %-10s %s\n", c.Name, c.Version)
			}
		}
		if !doctor.Healthy(checks) {
			return 1
		}
		return 0
```

Update the usage constant:

```go
const usage = `usage: cfo <command> [args]

commands:
  version   print the cfo version
  doctor    check the tools cfo needs (git, gh, claude, herdr, treehouse)
`
```

- [ ] **Step 8: Run all tests to verify they pass**

Run: `go test ./...`
Expected: PASS in every package.

- [ ] **Step 9: Commit**

```powershell
git add internal/doctor cmd/cfo
git commit -m "feat(doctor): required-tool checks behind cfo doctor"
```

---

### Task 8: Replace inherited bash CI with Go CI

**Files:**
- Delete: `.github/workflows/ci.yml`
- Delete: `.github/workflows/no-mistakes-required.yml`
- Delete: `.github/workflows/windows-herdr-spike.yml`
- Create: `.github/workflows/go.yml`

**Interfaces:**
- Consumes: the module and tests from Tasks 1-7.
- Produces: a `go` workflow later plans extend with the release build job.

- [ ] **Step 1: Delete the inherited workflows**

```powershell
git rm .github/workflows/ci.yml .github/workflows/no-mistakes-required.yml .github/workflows/windows-herdr-spike.yml
```

These exercise the bash layer this fork replaces; keeping them would burn CI minutes on red runs forever.

- [ ] **Step 2: Write the Go workflow**

Create `.github/workflows/go.yml`:

```yaml
name: go

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go vet ./...
      - run: go test ./...
      - run: go build ./cmd/cfo
```

- [ ] **Step 3: Verify the same gates locally**

Run: `go vet ./...` then `go test ./...` then `go build ./cmd/cfo`
Expected: vet silent, tests PASS, build succeeds.
Delete the build artifact again if it landed in the repo root: `Remove-Item cfo.exe -ErrorAction SilentlyContinue`.

- [ ] **Step 4: Commit and push, then confirm CI**

```powershell
git add .github/workflows/go.yml
git commit -m "chore(ci): replace inherited bash ci with go vet, test, build"
git push origin main
```

Then watch the run: `gh run watch --repo fpresta0607/code-goblins` (or `gh run list --repo fpresta0607/code-goblins --limit 1` until it shows `completed success`).
Expected: the `go` workflow completes green on `windows-latest`.

---

## Self-Review Notes

- Spec coverage in this plan: cfo.exe skeleton (section 2), state shapes and Windows file system rules (section 3), lock replacement (section 3), wake queue (section 3), doctor (section 7), CI (section 8). Deliberately deferred to later plans: hooks and watcher (section 4), Herdr and treehouse (section 5), AXI (section 6), install.ps1 and releases (section 7), instruction-layer rebrand (Naming section).
- Type consistency: `fsx.ReadLines` propagates `os.ErrNotExist`; `state.TailStatus` and `wake.Pending`/`readAll` convert absence to empty, `state.ReadMeta` propagates it (absence of a meta file is meaningful to callers, matching upstream's "no metadata for id" verdict).
- The wake ack floor lives in a sibling `.wake-ack` file so sequences survive a fully drained queue; both files are written atomically.
