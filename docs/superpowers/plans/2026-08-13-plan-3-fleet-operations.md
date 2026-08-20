# Plan 3: Fleet Operations Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

> **Superseded - treehouse removed.** The `internal/treehouse` package, its pooled-lease acquisition, and the `treehouse` doctor check were all removed: spawn now creates a plain git worktree at `<project>/.worktrees/gb-<id>` through `internal/worktree`, and cleanup removes it through `worktree.Service.Return`. Where the notes below point at `internal/treehouse`, read the `internal/worktree` doc comments and the "Project worktree environment" section of `AGENTS.md` instead. Task 2 and the treehouse text elsewhere remain historical and are not re-synced.

> **Superseded — treehouse acquisition and Herdr pane adapter.** The pane-scoped acquisition design this plan originally specified was replaced during implementation: spawn acquires a pooled worktree through `treehouse get --lease --json`, where the durable lease is the allocation evidence, and the `treehouse.Pane` / `herdr.Pane` / `RunPane` / `ForegroundCWD` adapters were removed as dead code. The affected sections are Task 2 (acquisition) and Task 3 (pane adapter); their authoritative owners are the `internal/treehouse` doc comments, `docs/superpowers/specs/2026-08-12-windows-native-fork-design.md` §10, and `docs/plans/2026-08-13-herdr-operational-compatibility-design.md`. Everything else below remains the historical plan and is not re-synced.

> **Superseded — native agent launch and cfo workspace.** Spawn now starts claude, codex, and kimi as named native Herdr agents via `herdr agent start` (harness args after `--`) and delivers their brief via `herdr agent prompt`; pi launches as a typed command in the prepared pane shell because Herdr's Windows agent start cannot run its npm `.cmd` shim. Spawn confirms each harness's declared trust dialog with adapter-specific keys (claude: Enter; pi: Enter; kimi: Up+Enter), and adopts or creates the flat workspace labeled `cfo` instead of `code-goblins`. The affected sections are Task 3 (`EnsureContainer` workspace label), Task 5 (`Launch` shape and `PowerShellLine`), and Task 7 (launch sequence); their authoritative owners are the `internal/herdr`, `internal/harness`, and `internal/spawn` doc comments plus `README.md` and `AGENTS.md`. Those task texts below remain historical and are not re-synced.

> **Superseded — monitor classification and wake cadence.** The Task 4 classification contract below (immediate `unchanged_idle` staleness, no idle grace, and no launch/parked/awaiting-answer states) was reworked during the spawn/wake hardening pass: a freshly launched pane now classifies as `launching`, a parked goblin as `parked`, a finished turn (agent_status `done`) as `awaiting_answer`, and a pane only reaches `stale` after a genuine idle/stall window. The authoritative owner is the `internal/monitor` doc comments; the Task 4 task text below remains historical and is not re-synced.

**Goal:** Build the Windows-native fleet operations needed to acquire isolated treehouse worktrees, run real Claude Code, Codex, and Pi sessions in Herdr, persist restart-safe task state, steer and inspect Code Goblins, and render one typed fleet view.

**Architecture:** Keep `cfo.exe` as the single entry point, keep Herdr and treehouse as external Windows binaries, and move orchestration, JSON parsing, state interpretation, and rendering into small Go packages.
The first Herdr implementation uses an injected `os/exec` wrapper for every CLI operation, while the watcher continues to rely on its existing polling fallback instead of introducing an unverified Windows socket client.
Treehouse remains the only worktree allocator; acquisition was superseded from pane-scoped `treehouse get` plus foreground-cwd polling to `treehouse get --lease --json` (see the superseded note at the top).

**Tech Stack:** Go 1.26.5, the standard library only, Windows `os/exec`, Herdr CLI JSON, treehouse CLI, Git CLI, and the existing `internal/fsx`, `internal/home`, `internal/lock`, and `internal/state` packages.

---

## Scope and locked decisions

The approved design is `docs/superpowers/specs/2026-08-12-windows-native-fork-design.md`.
The behavioral sources are `.superpowers/sdd/plan-3-research/treehouse.md`, `.superpowers/sdd/plan-3-research/herdr.md`, `.superpowers/sdd/plan-3-research/spawn.md`, and `.superpowers/sdd/plan-3-research/steer.md`.
The existing Plan 1 and Plan 2 packages are the implementation baseline, and their public behavior must remain green throughout this plan.

The following decisions are part of this plan and must not be reopened during implementation:

- Plan 3 accepts real Claude Code, Codex, and Pi dispatches on Windows, while Grok and OpenCode remain cut.
- Herdr uses one flat workspace per CFO home and one tab per Code Goblin.
- Presentation spaces, workspace projection, focus-preserving cleanup, and Herdr protocol-19 features are out of scope.
- Every Herdr request goes through a typed subprocess wrapper that supplies an explicit trailing `--session` flag.
- The initial Herdr client does not speak the control socket directly.
- Herdr push-event subscription is not required for Plan 3, and polling remains the durable supervision path until the Windows socket family is verified in a later task.
- Treehouse remains the sole allocator, and ordinary worker acquisition uses `treehouse get --lease --json` (the pane-scoped `treehouse get` contract was superseded; see the note at the top).
- Secondmates, Relay, and AFK are out of scope.
- `tasks-axi` and `quota-axi` remain subprocess integrations, and their internal protocols are not reimplemented in Go.
- Fresh metadata writes use the existing atomic `internal/fsx.AtomicWriteFile` primitive even where upstream used a direct shell redirection.
- Plan 3 supports one local ship or scout spawn at a time, and batch dispatch, secondmate dispatch, and remote dispatch remain later work.
- No task may launch into the primary checkout, and no failure may fall back to deleting an unreturned worktree.
- Plan 3 adds a read-only monitoring contract whose exact records, classifications, durable wake handoff, restart behavior, and boundaries are owned by Task 4.

## Shared implementation rules

Use table-driven tests for parsers, classifiers, flag mapping, and state transitions.
Use fake subprocess runners for deterministic unit tests, and reserve installed-tool tests for the opt-in Windows acceptance suite.
Use `context.Context` on every operation that can invoke an external process or wait for a pane state.
Return typed errors that preserve the failed operation, target, and external stderr without swallowing the original cause.
Treat an unreadable or ambiguous Herdr response as `unknown` and refuse destructive cleanup or successful delivery claims.
Keep all path comparisons absolute, cleaned, and case-insensitive on Windows.
Keep all state writes atomic and CRLF-tolerant by routing through the existing `internal/fsx` and `internal/state` primitives.
Task 4 is the sole owner for monitor record schemas, state paths, stale classification, and wake publication.
Do not edit `bin/`, `tests/`, `AGENTS.md`, or any legacy Bash file.

## Task 1: Add the subprocess and Windows path seams

**Files:**

- Create: `internal/execx/execx.go`
- Test: `internal/execx/execx_test.go`
- Create: `internal/fsx/path.go`
- Test: `internal/fsx/path_test.go`
- Modify: `internal/home/home.go`
- Test: `internal/home/home_test.go`

**Interfaces:**

```go
package execx

type Request struct {
    Dir string
    Env []string
    Name string
    Args []string
}

type Result struct {
    Stdout  []byte
    Stderr  []byte
    ExitCode int
}

type Runner interface {
    Run(ctx context.Context, req Request) (Result, error)
}

type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, req Request) (Result, error)
```

`OSRunner.Run` must use `exec.CommandContext`, set `cmd.Dir` only when `Request.Dir` is non-empty, preserve the caller's environment when `Request.Env` is nil, capture stdout and stderr separately, and return a non-nil error only for process start, context cancellation, or wait failures.
`Result.ExitCode` must preserve a normal non-zero child exit so callers can distinguish a tool refusal from an inability to start the tool.

`internal/fsx/path.go` must expose `AbsClean`, `Canonical`, and `SamePath` helpers.
`Canonical` must use `filepath.Abs`, `filepath.Clean`, and best-effort `filepath.EvalSymlinks` without treating an unavailable symlink resolution as a successful equality proof.
`SamePath` must compare canonicalized paths with `strings.EqualFold` after normalizing separators and long-path prefixes consistently.

**Step 1: Write the failing tests.**

Test that a fake runner receives the exact working directory, environment, executable, and argument vector.
Test that a non-zero child result is returned with its exit code and captured stderr.
Test that context cancellation returns an error and does not report a successful result.
Test CRLF and LF path inputs, drive-letter casing, separator differences, and a primary path that must not equal a linked worktree path.
Test that `home.IsPrimary` continues to use one `git rev-parse --git-dir --git-common-dir` call and remains false for linked worktrees.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/execx ./internal/fsx ./internal/home -count=1`
Expected: FAIL because `internal/execx` and the new path helpers do not exist.

**Step 3: Implement the minimal seams.**

Implement only the runner, path normalization, and the existing `home.IsPrimary` probe consolidation.
Do not add a generic process supervisor, shell abstraction, or external dependency.

**Step 4: Run the focused tests.**

Run: `go test ./internal/execx ./internal/fsx ./internal/home -count=1`
Expected: PASS.

**Step 5: Commit the implementation task.**

Run: `git add internal/execx internal/fsx/path.go internal/fsx/path_test.go internal/home/home.go internal/home/home_test.go && git commit -m "feat(exec): add Windows subprocess and path seams"`

## Task 2: Port treehouse acquisition, validation, freshening, and return

> **Superseded (acquisition):** the `treehouse.Pane` interface and `Service.Acquire(ctx, pane, project)` foreground-cwd polling below were replaced by `treehouse get --lease --json`, with the durable lease as the allocation evidence; `Acquire` now takes `(ctx, project, holder)` and the pane adapters were removed. See `internal/treehouse` and the spec §10.

**Files:**

- Create: `internal/treehouse/treehouse.go`
- Test: `internal/treehouse/treehouse_test.go`
- Create: `internal/treehouse/git.go`
- Test: `internal/treehouse/git_test.go`
- Modify: `internal/doctor/doctor.go`
- Test: `internal/doctor/doctor_test.go`

**Interfaces:**

```go
package treehouse

type Pane interface {
    Run(ctx context.Context, text string) error
    ForegroundCWD(ctx context.Context) (string, error)
}

type Git interface {
    WorktreeTop(ctx context.Context, dir string) (string, error)
    FetchAndFreshen(ctx context.Context, dir string) error
    Return(ctx context.Context, project, worktree string) error
}

type Service struct {
    Commands execx.Runner
    Git      Git
    Poll     time.Duration
    Timeout  time.Duration
    Sleep    func(context.Context, time.Duration) error
}

type Worktree struct {
    Path string
}

func (s Service) Acquire(ctx context.Context, pane Pane, project string) (Worktree, error)
func (s Service) Freshen(ctx context.Context, worktree string) error
func (s Service) Return(ctx context.Context, project, worktree string) error
func Validate(ctx context.Context, git Git, project, worktree string) error
```

`Acquire` must send the literal line `treehouse get` through `Pane.Run`, then poll `Pane.ForegroundCWD` for at most 60 iterations at one-second intervals.
The first non-project path is only accepted after two consecutive canonical reads agree, and a read that equals the canonical primary project resets the candidate.
The timeout error must include `treehouse get did not enter a worktree within 60s` and the Herdr target supplied by the caller.
`Validate` must require a readable directory, a Git top-level equal to the worktree itself, and a top-level different from the primary project.
`Freshen` must run `git fetch --quiet origin`, `git remote set-head origin --auto`, fetch the resolved default branch, require clean porcelain status, reset hard to the expected remote commit, and verify that `HEAD` equals the expected commit.
`Return` must invoke `treehouse return --force <worktree>` from the project directory and must never remove the worktree directly.
`Return` may retry only the exact `index.lock` collision signature up to the configured bounded count, and it must leave the worktree and lock untouched when Windows cannot prove the lock is stale.
The existing doctor must report `treehouse`, `claude`, `codex`, and `pi` as distinct required tools, while a missing optional adapter must be reported with an installation hint rather than silently selected.

**Step 1: Write the failing tests.**

Create a scripted fake pane whose cwd sequence covers project, stale foreign path, first candidate, and second candidate reads.
Assert that one agreeing non-project read is insufficient, two agreeing reads succeed, project resets the debounce, and timeout returns the exact diagnostic.
Test validation of a path that is a subdirectory, the primary checkout, a non-Git directory, and an isolated Git worktree.
Test the exact Git command sequence for freshening and refusal on dirty porcelain output.
Test that `treehouse return --force` runs with `Dir=project` and that only the exact index-lock error is retried.
Test doctor output for all three harness binaries and the existing tools.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/treehouse ./internal/doctor -count=1`
Expected: FAIL because the package and new doctor entries do not exist.

**Step 3: Implement the minimal treehouse service.**

Use the injected `execx.Runner` for every Git and treehouse subprocess.
Keep the poll interval and timeout injectable for tests while using one second and 60 seconds in production.
Use `internal/fsx.SamePath` for all Windows path identity decisions.
Use the existing atomic write and retry conventions for any temporary helper records.

**Step 4: Run the focused tests.**

Run: `go test ./internal/treehouse ./internal/doctor -count=1`
Expected: PASS.

**Step 5: Run the real treehouse prerequisite check on Windows.**

Run: `treehouse --version`
Expected: a successful Windows-native version response.

Run: `treehouse get --help`
Expected: help text containing `--lease` (Plan 3 later adopted `treehouse get --lease --json` for durable acquisition).

Record a failing Windows prerequisite as an acceptance blocker rather than adding a second worktree allocator.

## Task 3: Implement the flat Windows Herdr client

> **Superseded (pane adapter):** `RunPane`, `ForegroundCWD`, and the `herdr.Pane` adapter below were removed with the pane-cwd acquisition redesign; the "pane adapter" note in Step 3 no longer applies.

**Files:**

- Create: `internal/herdr/model.go`
- Create: `internal/herdr/client.go`
- Create: `internal/herdr/parse.go`
- Test: `internal/herdr/client_test.go`
- Test: `internal/herdr/parse_test.go`
- Modify: `internal/treehouse/treehouse.go`
- Test: `internal/treehouse/treehouse_test.go`

**Interfaces:**

```go
package herdr

type Target struct {
    Session string
    Pane    string
}

func ParseTarget(raw string) (Target, error)
func (t Target) String() string

type Container struct {
    Session          string
    WorkspaceID      string
    SeededDefaultTab string
}

type Endpoint struct {
    Target        Target
    WorkspaceID   string
    TabID         string
    PaneID        string
}

type Client struct {
    Commands execx.Runner
    Session  string
    Sleep    func(context.Context, time.Duration) error
}

func (c *Client) EnsureServer(ctx context.Context) error
func (c *Client) EnsureContainer(ctx context.Context, cwd string) (Container, error)
func (c *Client) CreateTask(ctx context.Context, container Container, label, cwd string) (Endpoint, error)
func (c *Client) RunPane(ctx context.Context, target Target, text string) error
func (c *Client) SendLiteral(ctx context.Context, target Target, text string) error
func (c *Client) SendKey(ctx context.Context, target Target, key string) error
func (c *Client) ForegroundCWD(ctx context.Context, target Target) (string, error)
func (c *Client) Capture(ctx context.Context, target Target, lines int, ansi bool) (string, error)
func (c *Client) AgentStatus(ctx context.Context, target Target) (AgentStatus, error)
func (c *Client) WaitForWorking(ctx context.Context, target Target, budget time.Duration, polls int) (SubmitState, error)
```

Every command must pass `--json` where the upstream contract reads JSON and must append `--session <session>` as the final Herdr arguments.
The client may also set `HERDR_SESSION` in the child environment for compatibility, but correctness must never depend on that environment variable.
`ParseTarget` must split on the first colon only so `default:w1:p2` becomes session `default` and pane `w1:p2`.
`EnsureServer` must read `.server.running`, start `herdr server` with `Start` semantics when needed, and poll status 20 times at 500 milliseconds before returning an error.
`EnsureContainer` must use the flat label `code-goblins`, call `workspace create --cwd <cwd> --label <label> --no-focus` when absent, and retain the exact seeded tab ID from the create response.
`CreateTask` must use label `gb-<id>`, refuse a duplicate tab unless its pane is structurally dead or has no registered agent, create with `tab create --workspace <id> --cwd <cwd> --label <label> --no-focus`, and prune only the exact seeded default tab after the real tab exists.
The seeded default tab may be closed only when the live tab ID still has label `1`, the workspace has at least two tabs, its pane is not working, and the close call succeeds or is safely ignored.
`Capture` must request at least 200 lines from Herdr and trim locally to the caller's requested line count.
`SendLiteral` must use `pane send-text`, `RunPane` must use `pane run`, and `SendKey` must use `pane send-keys` with the narrow key normalization from the extracted contract.
The liveness classifiers must distinguish pane absence, no registered agent, live agent, unreadable response, and busy or idle submit status without using process exit status as business-state evidence.
`WaitForWorking` must poll `agent get`, return immediately on a working response, return idle only when every read is idle, and return unknown only when every read fails.
Do not add the native event socket reader in this task.

**Step 1: Write the failing tests.**

Test target parsing with missing sessions, empty panes, and pane IDs containing multiple colons.
Test exact Herdr arguments, explicit session routing, separate stdout and stderr capture, and strict errors for missing JSON fields.
Test server startup and the 10-second bounded status loop with an injected clock and sleeper.
Test workspace adoption, duplicate-label refusal, husk replacement, seeded-tab pruning, and flat workspace labels.
Test the 200-line capture floor, local tail trimming, key normalization, and send primitive selection.
Test the complete liveness decision table, including `pane_not_found`, `agent_not_found`, malformed JSON, `working`, `idle`, `done`, `blocked`, and unknown statuses.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/herdr ./internal/treehouse -count=1`
Expected: FAIL because the Herdr package and its typed models do not exist.

**Step 3: Implement the subprocess-first Herdr client.**

Decode only the response fields consumed by the contract and reject absent IDs rather than guessing from labels or array order.
Keep the event-wait decision out of this client so the Windows transport boundary remains explicit.
Expose the Herdr pane adapter needed by `internal/treehouse.Service` through a small wrapper that implements `treehouse.Pane`.

**Step 4: Run the focused tests.**

Run: `go test ./internal/herdr ./internal/treehouse -count=1`
Expected: PASS.

**Step 5: Run the installed Herdr smoke probe.**

Run: `herdr status --json --session default`
Expected: valid JSON with a numeric client protocol and a server status that the client can reconcile.

Run: `herdr api schema --json --session default`
Expected: valid JSON or a documented capability response from the installed Windows preview.

Run: `herdr session list --json --session default`
Expected: the target session is addressable through the explicit session flag.

## Task 4: Add typed task metadata, status events, and crew-state resolution

**Files:**

- Create: `internal/state/task.go`
- Test: `internal/state/task_test.go`
- Create: `internal/crewstate/crewstate.go`
- Test: `internal/crewstate/crewstate_test.go`
- Create: `internal/monitor/record.go`
- Create: `internal/monitor/service.go`
- Test: `internal/monitor/record_test.go`
- Test: `internal/monitor/service_test.go`
- Modify: `internal/state/meta.go`
- Modify: `internal/state/status.go`
- Modify: `internal/watch/watch.go`
- Test: `internal/watch/watch_test.go`
- Modify: `internal/supervise/supervise.go`
- Test: `internal/supervise/supervise_test.go`

**Interfaces:**

```go
package state

type TaskMeta struct {
    ID                 string
    Window             string
    EndpointTaskID     string
    Worktree           string
    Project            string
    Harness            string
    Kind               string
    Mode               string
    Yolo               string
    TaskTmp            string
    Model              string
    Effort             string
    SpawnGen           string
    Backend            string
    HerdrSession       string
    HerdrWorkspaceID   string
    HerdrTabID         string
    HerdrPaneID        string
}

func ReadTaskMeta(stateDir, id string) (TaskMeta, error)
func WriteTaskMeta(stateDir string, meta TaskMeta) error
func ValidTaskID(id string) error
```

```go
package crewstate

type State string
type Source string

const (
    Working State = "working"
    Parked  State = "parked"
    Done    State = "done"
    Blocked State = "blocked"
    Paused  State = "paused"
    Failed  State = "failed"
    Unknown State = "unknown"
)

type Current struct {
    State  State
    Source Source
    Detail string
}

type Endpoint interface {
    Exists(ctx context.Context, target herdr.Target) (bool, error)
    BusyState(ctx context.Context, target herdr.Target) (herdr.BusyState, error)
}

func Resolve(ctx context.Context, stateDir, id string, endpoint Endpoint) (Current, error)
func ParseStatusLine(line string) (verb, detail string, ok bool)
func FoldOpenDecisions(lines []string) []Decision
```

```go
package monitor

const Schema = "cfo-monitor.v1"

type ProbeVerdict string

const (
    ProbePresent ProbeVerdict = "present"
    ProbeMissing ProbeVerdict = "missing"
    ProbeUnknown ProbeVerdict = "unknown"
)

type EndpointSample struct {
    Verdict  ProbeVerdict
    Endpoint herdr.Endpoint
    TabLabel string
    Agent    herdr.AgentStatus
    Capture  []byte
    Detail   string
}

type Health string

const (
    HealthActive  Health = "active"
    HealthBusy    Health = "busy"
    HealthIdle    Health = "idle"
    HealthPaused  Health = "paused"
    HealthStale   Health = "stale"
    HealthUnknown Health = "unknown"
)

type Reason string

const (
    None             Reason = "none"
    UnchangedIdle    Reason = "unchanged_idle"
    BusyTurnOverAge  Reason = "busy_turn_over_age"
    DeclaredPause    Reason = "declared_pause"
    EndpointMissing  Reason = "endpoint_missing"
    EndpointUnknown  Reason = "endpoint_unknown"
    InvalidRecord    Reason = "invalid_record"
)

type EventSource string

const (
    TaskEvent      EventSource = "task"
    HeartbeatEvent EventSource = "heartbeat"
)

type Event struct {
    Source EventSource `json:"source"`
    TaskID string      `json:"task_id,omitempty"`
    Kind   string      `json:"kind"`
    Key    string      `json:"key"`
    Detail string      `json:"detail"`
}

type Observation struct {
    Schema               string       `json:"schema"`
    TaskID               string       `json:"task_id"`
    Endpoint             string       `json:"endpoint"`
    EndpointVerdict      ProbeVerdict `json:"endpoint_verdict"`
    Digest               string       `json:"digest"`
    LastObserved         time.Time    `json:"last_observed"`
    LastSeen             time.Time    `json:"last_seen"`
    LastProgress         time.Time    `json:"last_progress"`
    StaleSince           *time.Time   `json:"stale_since,omitempty"`
    NextEscalation       *time.Time   `json:"next_escalation,omitempty"`
    NextPauseResurface   *time.Time   `json:"next_pause_resurface,omitempty"`
    Health               Health       `json:"health"`
    Reason               Reason       `json:"reason"`
    Escalation           int          `json:"escalation"`
    DemandDeepInspection bool         `json:"demand_deep_inspection"`
    PendingEvent         *Event       `json:"pending_event,omitempty"`
}

type Heartbeat struct {
    Schema         string     `json:"schema"`
    LastCycle      time.Time  `json:"last_cycle"`
    LastHeartbeat  time.Time  `json:"last_heartbeat"`
    NoChangeStreak int        `json:"no_change_streak"`
    NextDue        time.Time  `json:"next_due"`
    PendingEvent   *Event     `json:"pending_event,omitempty"`
}

type Prober interface {
    Inspect(ctx context.Context, meta state.TaskMeta) (EndpointSample, error)
}

type Service struct {
    StateDir              string
    Probe                 Prober
    Now                   func() time.Time
    StaleEscalateAfter    time.Duration
    BusyTurnMax           time.Duration
    PauseResurfaceAfter   time.Duration
    DemandInspectionAfter int
    Heartbeat             time.Duration
    HeartbeatMax          time.Duration
}

type ScanResult struct {
    Observations []Observation
    Heartbeat    Heartbeat
    Event        *Event
}

func ObservationPath(stateDir, id string) string
func HeartbeatPath(stateDir string) string
func ReadObservation(stateDir, id string) (Observation, error)
func WriteObservation(stateDir string, observation Observation) error
func ReadHeartbeat(stateDir string) (Heartbeat, error)
func WriteHeartbeat(stateDir string, heartbeat Heartbeat) error
func (s Service) Scan(ctx context.Context) (ScanResult, error)
func (s Service) Publish(event Event) (wake.Record, error)
```

`TaskMeta` must preserve the upstream flat key names, write `backend=herdr` and all Herdr IDs for Herdr tasks, and reject task IDs that are empty, begin with `.`, contain characters outside `A-Za-z0-9._-`, or exceed 64 bytes.
`ReadTaskMeta` must retain last-value-wins for compatibility, while `WriteTaskMeta` must emit a deterministic map through `state.WriteMeta` and `fsx.AtomicWriteFile`.
`Resolve` must implement the Plan 3 state order of missing metadata, missing worktree, unreadable or absent endpoint, exact Herdr busy state, and the last usable status-log line.
`Resolve` may use a status-log line only after the live probe structurally validates the metadata's exact session, workspace, tab, pane, task-tab label, and registered agent, and then reports exact idle.
`Resolve` must return `unknown` for a missing, malformed, unreadable, or ambiguous endpoint and must not trust the status log in those cases.
The status-log mapping must produce `working`, `parked`, `blocked`, `paused`, `done`, `failed`, or `unknown`, with `needs-decision` mapping to `parked` and `resolved` never becoming a current state.
Plan 3 must not invoke `no-mistakes` run attribution because that CLI is not a Windows-native Plan 3 dependency.
`FoldOpenDecisions` must preserve keyed `needs-decision` and `blocked` records and close them only with matching `resolved` or `captain-held` events, but no secondmate-specific reserved namespaces are needed.

The monitor record paths are exactly `<CFO_HOME>\state\monitor\tasks\<id>.json` and `<CFO_HOME>\state\monitor\heartbeat.json`.
Every record is encoded with `Schema`, decoded with unknown-field rejection, and atomically replaced through `fsx.AtomicWriteFile`.
The monitor must not read, write, or translate upstream shell marker filenames.
Before every classification, `Scan` loads the task's prior observation and the fleet heartbeat record.
A missing record establishes a fresh baseline, while an unreadable, malformed, or unsupported record remains intact, classifies the task as `unknown`, and produces an actionable `stale` event with reason `invalid_record`.
`EndpointSample.Verdict` is `ProbePresent` only when the live Herdr response exactly matches every metadata endpoint ID, target session, and `gb-<id>` tab label and exposes a well-formed registered-agent state.
The monitor stores only the SHA-256 digest of that validated pane's 200-line capture, never the capture text.
A changed digest records `active`, sets `LastSeen` and `LastObserved`, and clears `StaleSince`, `Escalation`, and `DemandDeepInspection`.
An unchanged exact-idle pane records `stale` with `unchanged_idle`, publishes one `stale` event without an escalation, and preserves its original `StaleSince`.
After that stale event is published, the same unchanged observation must not publish again until its persisted `NextEscalation` is due.
An exact-busy pane remains `busy` and emits no stale event until the newer of `state/<id>.turn-ended` and the task metadata write is older than `BusyTurnMax`.
Once that busy-progress bound is crossed, the unchanged pane records `stale` with `busy_turn_over_age` and begins the same stale timer.
Each elapsed `NextEscalation` interval increments `Escalation` once, advances that timestamp by `StaleEscalateAfter`, and reaching `DemandInspectionAfter` sets `DemandDeepInspection` and adds `demand-deep-inspection` to the event detail.
A declared `paused` current state records `paused` with `declared_pause` and may re-surface only at `PauseResurfaceAfter`, never as a wedge escalation.
`Missing`, `Unknown`, and probe errors always record `unknown`, publish a `stale` event that names the endpoint problem, and never become `idle`, `healthy`, or status-log fallback evidence.
The heartbeat record updates `LastCycle` on every scan and persists `LastHeartbeat`, `NoChangeStreak`, and `NextDue` on every due heartbeat.
A no-change heartbeat doubles the next interval up to `HeartbeatMax` and emits no wake, while a due heartbeat that discovers an otherwise unsurfaced actionable observation records a `heartbeat` event and resets the streak.
`Scan` persists a pending event in its observation or heartbeat record before returning it.
`Publish` calls `wake.Append`, then `wake.PublishEpisode`, and only then clears the matching pending event so a restart can retry after any interrupted handoff without losing evidence.
The monitor is inspection-only and must not invoke send, key, interrupt, kill, restart, close-tab, treehouse-return, file-delete, or worktree-delete actions.
`internal/watch/watch.go` must call `Service.Scan` after raw status-signal handling and before its wait, use `ScanResult.Event` as the one actionable close reason, and replace its monitor-specific heartbeat and liveness markers with the typed heartbeat record.
`internal/supervise/supervise.go` must read the typed heartbeat's `LastCycle` for watcher-health checks.

**Step 1: Write the failing tests.**

Test all task ID validation boundaries and CRLF metadata parsing.
Test deterministic metadata writes, Herdr-specific fields, default `kind=ship`, and absent optional fields.
Test each current-state precedence branch with a fake endpoint and status log, including the refusal to use a log after an unknown endpoint or anything other than exact structural idle.
Test status lines with before-colon and after-colon decision keys, invalid keys, resolution, and unrelated terminal events.
Test strict monitor JSON decoding, exact Windows monitor paths, atomic writes, missing-record baselines, and preservation of corrupt records.
Test changed and unchanged capture digests, busy protection, busy-turn expiration, immediate idle stale classification, one-per-interval escalation, deep-inspection threshold, paused re-surfacing, and unknown endpoint classification.
Test restart recovery by constructing a new `Service` over persisted records and proving that last-seen time, stale duration, escalation count, deep-inspection state, heartbeat backoff, and next due time continue without an in-memory reset.
Test `Publish` writes the queue record before the recovery episode, leaves the matching pending event and durable queue record readable if episode publication fails, and never publishes when record persistence failed.
Test that the monitoring fake receives no lifecycle, send, treehouse return, or delete operation.
Test watcher and supervisor health read the typed heartbeat and do not require monitor-specific shell marker filenames.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/state ./internal/crewstate ./internal/monitor ./internal/watch ./internal/supervise -count=1`
Expected: FAIL because the typed task metadata, crew-state, and monitor packages do not exist.

**Step 3: Implement the typed persistence and resolver.**

Keep the existing generic `ReadMeta`, `WriteMeta`, `AppendStatus`, and `TailStatus` APIs intact for Plan 2 callers.
Build the typed task layer on top of them instead of introducing a second task-metadata format.
Return `Current{State: Unknown, Source: None}` for ambiguous external reads and let the monitor publish the human-inspection wake.
Keep monitor evidence in its dedicated typed records and preserve the existing durable wake queue as the only wake transport.

**Step 4: Run the focused tests.**

Run: `go test ./internal/state ./internal/crewstate ./internal/monitor ./internal/watch ./internal/supervise -count=1`
Expected: PASS.

## Task 5: Add typed Claude, Codex, and Pi harness adapters

**Files:**

- Create: `internal/harness/adapter.go`
- Create: `internal/harness/windows.go`
- Create: `internal/harness/claude.go`
- Create: `internal/harness/codex.go`
- Create: `internal/harness/pi.go`
- Test: `internal/harness/adapter_test.go`
- Test: `internal/harness/claude_test.go`
- Test: `internal/harness/codex_test.go`
- Test: `internal/harness/pi_test.go`

**Interfaces:**

```go
package harness

type Kind string

const (
    Claude Kind = "claude"
    Codex  Kind = "codex"
    Pi     Kind = "pi"
)

type LaunchSpec struct {
    BriefPath       string
    TaskTmp         string
    TurnEndedPath   string
    Model           string
    Effort          string
    PiExtensionPath string
}

type Launch struct {
    Executable string
    Args       []string
    Env        map[string]string
    PromptFile string
}

type Adapter interface {
    Kind() Kind
    Validate(ctx context.Context, runner execx.Runner) error
    Build(spec LaunchSpec) (Launch, error)
}

type Registry struct {
    Adapters map[Kind]Adapter
}

func DefaultRegistry() Registry
func (r Registry) Get(kind Kind) (Adapter, error)
func (l Launch) PowerShellLine() (string, error)
```

Adapters must return structured executable and argument data first, and only the final Herdr delivery step may render a PowerShell command line.
`PowerShellLine` must use single-quoted literals with doubled single quotes for paths and values, must set `GOTMPDIR` through `$env:GOTMPDIR`, and must never concatenate an unquoted user-controlled path into a command.
The Claude adapter must emit `CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false`, `claude`, `--dangerously-skip-permissions`, supported model and effort flags, and the brief content as the final prompt expression.
The Codex adapter must emit `codex`, `--dangerously-bypass-approvals-and-sandbox`, the supported model flag, and `-c model_reasoning_effort="<level>"` for supported non-default effort values without inventing a Bash-only notify command on Windows.
The Pi adapter must probe `pi --help`, include `--tui-mode regular` only when advertised, map only flags advertised by the installed binary, and reject a requested unsupported model or effort rather than silently dropping it.
The Pi extension path is optional in Plan 3 and is passed only when a later adapter extension is explicitly configured.
All three adapters must default to a brief prompt read from the absolute `BriefPath`, and all three must validate their executable before spawn.

**Step 1: Write the failing tests.**

Test registry lookup and refusal of Grok, OpenCode, raw commands, and unknown harness names.
Test exact Claude, Codex, and Pi launch structures for default and explicit model and effort values.
Test Codex effort mapping and refusal of the unsupported `max` mapping described by the upstream contract.
Test Pi help-driven optional flag selection with fake `--help` output.
Test PowerShell quoting for apostrophes, spaces, percent signs, and paths containing backslashes.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/harness -count=1`
Expected: FAIL because the typed adapters do not exist.

**Step 3: Implement the typed adapters.**

Keep vendor-specific flag decisions in the vendor adapter files and keep Windows quoting in `windows.go`.
Do not add a common prompt protocol, a shell parser, or an adapter for an excluded harness.

**Step 4: Run the focused tests.**

Run: `go test ./internal/harness -count=1`
Expected: PASS.

## Task 6: Keep tasks-axi and quota-axi as subprocess integrations

**Files:**

- Create: `internal/axi/axi.go`
- Test: `internal/axi/axi_test.go`
- Modify: `internal/doctor/doctor.go`
- Test: `internal/doctor/doctor_test.go`

**Interfaces:**

```go
package axi

type Tasks struct {
    Commands execx.Runner
}

func (t Tasks) ShowFull(ctx context.Context, id string) (string, error)

type Quota struct {
    Commands execx.Runner
}

func (q Quota) JSON(ctx context.Context) ([]byte, error)
```

`Tasks.ShowFull` must invoke `tasks-axi show <id> --full` without parsing or rewriting the task body.
`Quota.JSON` must invoke `quota-axi --json` and return the raw JSON for the dispatch layer or a future policy owner.
Neither wrapper may infer provider credentials, model support, quota windows, or authentication state.
Doctor must list both AXI binaries as external requirements with concise installation hints.

**Step 1: Write the failing tests.**

Assert exact command names, arguments, working directory, raw stdout preservation, and non-zero exit propagation.
Assert that no JSON parsing occurs in the quota wrapper and no task-body normalization occurs in the tasks wrapper.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/axi ./internal/doctor -count=1`
Expected: FAIL because the wrappers do not exist.

**Step 3: Implement the thin wrappers.**

Do not add a quota selector or tasks backend in this task.

**Step 4: Run the focused tests.**

Run: `go test ./internal/axi ./internal/doctor -count=1`
Expected: PASS.

## Task 7: Implement one-task spawn orchestration

**Files:**

- Create: `internal/spawn/spawn.go`
- Test: `internal/spawn/spawn_test.go`
- Modify: `internal/state/task.go`
- Modify: `internal/lock/lock.go`

**Interfaces:**

```go
package spawn

type Request struct {
    ID          string
    Project     string
    BriefPath   string
    Kind        string
    Mode        string
    Yolo        bool
    Harness     harness.Kind
    Model       string
    Effort      string
    Session     string
}

type Result struct {
    Meta    state.TaskMeta
    Endpoint herdr.Endpoint
    Output  string
}

type Service struct {
    Herdr     *herdr.Client
    Treehouse treehouse.Service
    Harness   harness.Registry
    StateDir  string
    Project   string
    Sleep     func(context.Context, time.Duration) error
}

func (s Service) Spawn(ctx context.Context, req Request) (Result, error)
```

`Spawn` must validate the task ID before touching the filesystem, require an existing brief, validate the delivery mode line when present, acquire a per-task spawn lock, ensure the flat Herdr container, create the `gb-<id>` tab in the primary project, acquire and validate the treehouse worktree, freshen it, resolve the typed harness, build the Windows launch line, and publish metadata atomically.
The launch sequence must set `GOTMPDIR=<tasktmp>\gotmp`, send the launch line literally, send Enter as a separate key, and confirm a working agent before reporting success.
The metadata must contain `window`, `endpoint_task_id`, `worktree`, `project`, `harness`, `kind`, `mode` for ship tasks, `yolo` for ship tasks, `tasktmp`, `model`, `effort`, `spawn_gen`, `backend=herdr`, `herdr_session`, `herdr_workspace_id`, `herdr_tab_id`, and `herdr_pane_id`.
The success line must be `spawned <id> harness=<name> kind=<kind> mode=<mode> yolo=<on|off> window=<target> worktree=<path>` for ship tasks and must omit mode and yolo for scouts.
On launch failure, append a bounded `failed:` status, return the exact cause, and leave the acquired worktree available for explicit treehouse return instead of deleting it.
No secondmate, remote, batch, tmux, zellij, cmux, Orca, Relay, or AFK path may be added here.

**Step 1: Write the failing tests.**

Test refusal before filesystem mutation for invalid IDs, missing briefs, mode mismatch, unsupported harness, dirty worktree, and missing Herdr IDs.
Test lock contention and release, task metadata contents, tasktmp creation, and exact success output.
Test the launch ordering with fake Herdr, treehouse, and harness dependencies.
Test launch verification failure records `failed:` and does not remove or rewrite the primary checkout.
Test scout metadata omits ship-only mode and yolo fields.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/spawn -count=1`
Expected: FAIL because the spawn package does not exist.

**Step 3: Implement the orchestration.**

Use existing named lock primitives and atomic metadata writes.
Keep dependency interfaces injected so all ordering and failure tests stay deterministic.

**Step 4: Run the focused tests.**

Run: `go test ./internal/spawn ./internal/state ./internal/herdr ./internal/treehouse -count=1`
Expected: PASS.

## Task 8: Implement target resolution, send, and peek

**Files:**

- Create: `internal/fleet/target.go`
- Create: `internal/fleet/send.go`
- Create: `internal/fleet/peek.go`
- Test: `internal/fleet/target_test.go`
- Test: `internal/fleet/send_test.go`
- Test: `internal/fleet/peek_test.go`

**Interfaces:**

```go
package fleet

type TargetResolver interface {
    Resolve(ctx context.Context, raw string) (herdr.Target, state.TaskMeta, error)
}

type Sender struct {
    Resolve TargetResolver
    Herdr   *herdr.Client
    Sleep   func(context.Context, time.Duration) error
}

func (s Sender) Text(ctx context.Context, raw string, message string) error
func (s Sender) Key(ctx context.Context, raw string, key string) error

type Peeker struct {
    Resolve TargetResolver
    Herdr   *herdr.Client
}

func (p Peeker) Tail(ctx context.Context, raw string, lines int) (string, error)
```

Use one shared resolver for Plan 3 because secondmate routing and `--resolve-key` are cut, but retain the explicit-target escape hatch for a canonical Herdr target containing a colon.
Task IDs and `gb-<id>` selectors must resolve through local metadata, and a bare Herdr pane ID must be rejected with an instruction to provide `<session>:<pane-id>`.
Text sends must type once with `pane send-text`, wait the configured settle duration, send Enter up to the configured retry count, and confirm delivery from the Herdr agent or composer state without treating `pending` or `unknown` as success.
Key sends must normalize Enter, Escape, Ctrl-C, and Ctrl-U through the typed Herdr mapping and must not accept text and `--key` in the same command.
Slash messages and Codex `$` messages must use the 1.2-second pre-Enter settle, and all other messages must use the 0.3-second settle.
Peek must default to 40 lines, request Herdr's 200-line minimum, and print only the trimmed terminal tail.
The `--resolve-key`, pending-reply, from-firstmate marker, and Muse-only interrupt-clear path are intentionally rejected or omitted in Plan 3.

**Step 1: Write the failing tests.**

Test task-ID, `gb-<id>`, explicit Herdr target, unknown selector, and bare-pane collision resolution.
Test text and key argument exclusivity, settle selection, Enter retry behavior, confirmed delivery, pending delivery refusal, and unknown delivery refusal.
Test peek default, invalid line count fallback to 200, exact Herdr capture arguments, and local tail output.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/fleet -count=1`
Expected: FAIL because the target, send, and peek packages do not exist.

**Step 3: Implement the shared resolver and Herdr-only operations.**

Keep send and peek behavior as separate methods over the shared resolver, and do not reintroduce upstream's secondmate-only branches.

**Step 4: Run the focused tests.**

Run: `go test ./internal/fleet -count=1`
Expected: PASS.

## Task 9: Implement typed fleet snapshots and human rendering

**Files:**

- Create: `internal/fleet/snapshot.go`
- Create: `internal/fleet/view.go`
- Create: `internal/fleet/backlog.go`
- Test: `internal/fleet/snapshot_test.go`
- Test: `internal/fleet/view_test.go`
- Test: `internal/fleet/backlog_test.go`

**Interfaces:**

```go
package fleet

type Snapshot struct {
    Schema      string           `json:"schema"`
    Home        string           `json:"home"`
    Tasks       []TaskRow        `json:"tasks"`
    Backlog     BacklogRows      `json:"backlog"`
    Secondmates []SecondmateRow  `json:"secondmates"`
}

type SecondmateRow struct{}

type TaskRow struct {
    ID            string            `json:"id"`
    Current       crewstate.Current `json:"current_state"`
    Monitor       MonitorSummary    `json:"monitor"`
    Kind          string            `json:"kind"`
    Project       string            `json:"project"`
    Backend       string            `json:"backend"`
    Endpoint      EndpointSummary   `json:"endpoint"`
    Artifact      string            `json:"artifact"`
    Path          string            `json:"path"`
    Actions       Actions           `json:"actions"`
}

type MonitorSummary struct {
    Health               monitor.Health `json:"health"`
    StaleSeconds         int64          `json:"stale_seconds"`
    LastSeen             *time.Time     `json:"last_seen"`
    Escalation           int            `json:"escalation"`
    DemandDeepInspection bool           `json:"demand_deep_inspection"`
}

func BuildSnapshot(ctx context.Context, h home.Home, endpoint EndpointReader) (Snapshot, error)
func RenderJSON(w io.Writer, snapshot Snapshot) error
func RenderMarkdown(w io.Writer, snapshot Snapshot) error
```

Use a concrete empty `[]SecondmateRow` slice because no secondmate rows are produced in Plan 3, and keep the JSON schema typed rather than building JSON strings.
The schema name must be `fleet-snapshot.v1`.
Tasks must be sorted by ID, while queued and done backlog records must preserve file order.
The task table must include ID, current state and source, health, stale duration, last-seen time, escalation, deep-inspection state, kind, project, backend, endpoint existence, artifact, path, and the peek action.
The snapshot builder must derive `MonitorSummary` from the Task 4 observation record once per task and convert a missing or invalid record to typed `unknown` values.
Neither renderer may read endpoints, pane output, monitor files, status logs, or clocks after receiving `Snapshot`.
The human renderer must preserve the upstream headings `# Fleet View`, `## Under Way`, `## Queued`, `## Done`, and `## Secondmates`, and must print the Plan 3 boundary `Secondmates are not supported in Plan 3.` in the final section.
The backlog parser must recognize `- [ ] <id> - <text>` and `- **<id>** - <text>` rows, derive titles with the documented URL and trailing-metadata cleanup, and preserve unstructured rows as non-table records.
The renderer must print `-` for missing values and must never read terminal output separately from the typed snapshot builder.

**Step 1: Write the failing tests.**

Test deterministic task sorting, metadata defaults, Herdr endpoint summary, monitor summary values, path preference, artifact preference, and action strings.
Test queued and done section parsing, order preservation, blocker formatting, title cleanup, and unstructured rows.
Test JSON round-trip and exact human headings, monitor columns, dash fallback text, and the Plan 3 secondmate boundary.
Build one snapshot containing active, stale, and unknown monitor records, then assert the JSON and Markdown projections expose identical health, stale duration, last-seen, escalation, and deep-inspection values from that one value.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./internal/fleet -count=1`
Expected: FAIL because snapshot, backlog, and renderer types do not exist.

**Step 3: Implement the typed snapshot and renderer.**

Keep filesystem reads in the snapshot builder and keep rendering pure over `Snapshot`.
Use the existing `crewstate.Resolve` result as the only current-state source.
Use Task 4 records as the only monitor-health source.

**Step 4: Run the focused tests.**

Run: `go test ./internal/fleet -count=1`
Expected: PASS.

## Task 10: Wire the `cfo` commands and doctor output

**Files:**

- Create: `cmd/cfo/spawn.go`
- Create: `cmd/cfo/send.go`
- Create: `cmd/cfo/peek.go`
- Create: `cmd/cfo/fleet.go`
- Modify: `cmd/cfo/main.go`
- Modify: `internal/doctor/doctor.go`
- Test: `cmd/cfo/main_test.go`
- Test: `cmd/cfo/spawn_test.go`
- Test: `cmd/cfo/send_test.go`
- Test: `cmd/cfo/peek_test.go`
- Test: `cmd/cfo/fleet_test.go`

Add these command forms to the usage text:

```text
cfo spawn <id> --project <path> --brief <path> --harness <claude|codex|pi> [--mode <no-mistakes|direct-PR|local-only>] [--model <model>] [--effort <level>] [--yolo]
cfo send <target> [--key <key>] <text...>
cfo peek <target> [lines]
cfo fleet-view [--json]
```

`runSpawn` must resolve `CFO_HOME`, default the Herdr session from `HERDR_SESSION` or `default`, and pass all validated values into `spawn.Service`.
`runSend` and `runPeek` must stream only the command's user-facing output and send diagnostics to stderr.
`runFleet` must reject unknown flags, use `RenderJSON` for `--json`, and use `RenderMarkdown` otherwise.
The existing `doctor` command must show the new AXI and harness checks without changing the existing hook-pairing behavior.

**Step 1: Write the failing command tests.**

Test usage text, unknown command and flag exit codes, dependency injection through `CFO_HOME`, JSON versus Markdown fleet output, and propagation of spawn, send, and peek failures.
Test that the command layer does not create state for invalid or inert invocations.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./cmd/cfo ./internal/doctor -count=1`
Expected: FAIL because the new command cases and handlers do not exist.

**Step 3: Implement the command adapters.**

Keep CLI parsing in `cmd/cfo` and keep orchestration in internal packages.
Do not make command handlers depend on global mutable state beyond the existing environment-driven home resolution.

**Step 4: Run the focused tests.**

Run: `go test ./cmd/cfo ./internal/doctor -count=1`
Expected: PASS.

## Task 11: Add Windows integration fixtures and real-session acceptance

**Files:**

- Create: `tests/acceptance/plan3_windows.ps1`
- Create: `cmd/cfo/e2e_fleet_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `README.md`

The acceptance script must be opt-in and must refuse to run unless `CFO_PLAN3_REAL=1` is present.
The fixture must use a disposable Windows Git repository outside the primary checkout, a dedicated Herdr session name, and a dedicated treehouse pool or project clone.
The fixture must not use the production repository worktree for any worker.
The script must clean up only paths it created under its disposable root and must call `treehouse return --force` through `cfo` cleanup before deleting the fixture.

The acceptance sequence must be:

1. Run `go vet ./...` and `go test ./... -count=1`.
2. Build `cfo.exe` with `go build -o <fixture>\cfo.exe ./cmd/cfo`.
3. Run `cfo doctor` and require Git, Herdr, treehouse, Claude, Codex, and Pi checks to be usable for the real-session acceptance target, while recording tasks-axi and quota-axi as separate external integration checks.
4. Start a dedicated Herdr session and verify `herdr status --json --session <session>` and `herdr session list --json --session <session>` succeed.
5. Dispatch one Code Goblin with `--harness claude`, a benign brief that prints a unique acceptance marker, and the dedicated Herdr session.
6. Assert that the metadata points to a non-primary isolated worktree, the Herdr target splits on the first colon, and the flat workspace contains exactly one task tab plus any safely retained default tab.
7. Run `cfo peek gb-<id>` and require the marker or a documented startup-progress response.
8. Run `cfo send gb-<id> "print the acceptance marker and exit"` and require a confirmed delivery result.
9. Run `cfo fleet-view --json` and `cfo fleet-view`, and require the same task, endpoint, worktree, current-state, health, stale duration, last-seen, escalation, and deep-inspection information in both views.
10. Repeat steps 5 through 9 for `--harness codex` and `--harness pi`, and fail the real-session acceptance if either binary is unavailable.
11. With a short fixture-only monitor cadence, prove a visibly busy task remains protected from stale classification before `BusyTurnMax`, then prove an unchanged idle task produces a stale wake, increments its escalation on the next stale interval, and reports a deliberately nonexistent endpoint as `unknown`.
12. Start a fresh `cfo watch` process, prove the persisted stale observation and heartbeat record retain their prior values, and rerun both fleet views to require JSON and Markdown parity for those monitor values.
13. Verify every stale or unknown inspection leaves the corresponding Herdr task tab visible and the treehouse worktree present, then use `cfo drain` to confirm the durable wake records before acknowledgement.
14. Return every treehouse worktree through the project checkout and verify the primary checkout remains clean and is never recorded as a worker worktree.

The Go e2e test must use fake Herdr and treehouse binaries for deterministic CI coverage and must reserve real installed-tool execution for the PowerShell acceptance script.
The fake Herdr fixture must expose the task tabs in its workspace JSON and give the monitor a controllable clock, capture digest, agent state, and endpoint verdict.
The fake e2e must prove busy protection, stale escalation and deep-inspection state, unknown endpoint handling, persisted heartbeat backoff, restart recovery, durable stale and heartbeat wake publication, no automatic lifecycle or delete command, visible Herdr tabs, and JSON and Markdown parity.
The PowerShell acceptance must make the same assertions against the disposable Herdr session and worktrees, with visible surviving tabs and worktrees as the proof that monitoring did not auto-kill or delete anything.
The GitHub workflow must run the existing unit and build checks on `windows-latest` and must not require credentials or real harness binaries in ordinary CI.
README changes must document the opt-in real-session command, the disposable-environment requirement, and the fact that the Go toolchain is not needed for installed users.

**Step 1: Write the failing e2e and acceptance assertions.**

Test the fake-binary path end to end through `cfo spawn`, `cfo send`, `cfo peek`, and `cfo fleet-view`.
Test primary-checkout protection, restart-safe metadata, and cleanup refusal on an ambiguous treehouse return.
Test the PowerShell script's environment gate and path containment checks.
Test the fake monitor's busy protection, first stale wake, repeat stale escalation, deep-inspection threshold, unknown endpoint wake, heartbeat persistence, restart load, durable queue and episode handoff, and absence of lifecycle or delete calls.
Test that fake workspace JSON and the real acceptance script both require the Herdr task tab to remain visible after monitoring classification.
Test that both fleet renderers use the same snapshot values for monitor health, stale duration, last seen, escalation, and deep inspection.

**Step 2: Run the tests to verify they fail.**

Run: `go test ./cmd/cfo -run TestFleetEndToEnd -count=1 -v`
Expected: FAIL because the new commands and fixture are not implemented.

**Step 3: Implement the deterministic e2e harness and opt-in script.**

Keep real tool requirements out of ordinary Go tests and make every skipped real-tool leg visible in the acceptance report.

**Step 4: Run the full local verification battery.**

Run: `go vet ./...`
Expected: PASS with no diagnostics.

Run: `go test ./... -count=1`
Expected: PASS with zero failures.

Run: `go build ./cmd/cfo`
Expected: PASS and produce the ignored local `cfo.exe`.

Run: `$env:CFO_PLAN3_REAL='1'; powershell -NoProfile -ExecutionPolicy Bypass -File tests/acceptance/plan3_windows.ps1`
Expected: PASS for Claude, Codex, and Pi, with a missing harness treated as an acceptance blocker and no primary-checkout mutation.

## Reviewable acceptance criteria

Plan 3 is ready for review only when every criterion below has fresh command evidence:

- `go vet ./...` passes.
- `go test ./... -count=1` passes.
- `go build ./cmd/cfo` passes.
- The fake-binary end-to-end test exercises spawn, send, peek, fleet JSON, fleet Markdown, metadata, status, monitor heartbeat persistence, stale escalation, durable wakes, and restart reads.
- Treehouse acquisition leases a pooled worktree through `treehouse get --lease --json` (the durable lease is the allocation evidence) and rejects the primary checkout or a non-root Git directory.
- Herdr requests always use explicit session routing, strict JSON IDs, a flat workspace, one tab per task, and the 200-line capture floor.
- Claude, Codex, and Pi each have typed flag mapping tests and an installed-binary validation path.
- Task metadata is atomic, deterministic, CRLF-tolerant, and restart-readable.
- Crew-state resolution refuses to report success for unreadable endpoints and does not invent no-mistakes run attribution.
- The monitor uses only `state\monitor\tasks\<id>.json` and `state\monitor\heartbeat.json` for new monitoring evidence, reloads them before classification, and never uses upstream shell marker filenames.
- Busy protection, stale escalation, deep-inspection state, unknown endpoint handling, heartbeat persistence, and durable wake publication are proven by deterministic e2e coverage and opt-in Windows acceptance.
- Monitoring never kills, interrupts, restarts, closes, returns, or deletes a worker, endpoint, tab, worktree, or file.
- `cfo send` reports success only after confirmed submission, and `cfo peek` prints only the requested terminal tail.
- `cfo fleet-view --json` and `cfo fleet-view` derive health, stale duration, last-seen, escalation, and deep-inspection state from the same typed snapshot.
- No Plan 3 code touches secondmates, Relay, AFK, presentation spaces, or excluded backends and harnesses.
- `tasks-axi` and `quota-axi` remain thin subprocess wrappers with no provider or quota policy duplicated in Go.
- The real Windows acceptance run leaves the primary checkout clean and returns every acquired worktree through treehouse.

## Final review and commit

Before claiming completion, reread this plan and the modified design document, then inspect `git diff --check` and `git status --short`.
Confirm that the implementation branch contains no edits to `bin/`, `tests/`, `AGENTS.md`, or generated files.
Run the full verification battery from Task 11 after the final review.
Use a concise conventional implementation commit such as `feat(fleet): add Windows-native Herdr fleet operations` only after all acceptance evidence is recorded.
