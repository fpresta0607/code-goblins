# First Mate review for the CFO

Task: cfo-dispatch-profiles, Overlord direction of 2026-09-17.
Subject: `kunchenguid/firstmate` at HEAD `3eb5b63`, cloned read-only at `projects/firstmate`.
Nothing was pushed, filed, or changed there.
Every claim below names the First Mate file it was read from; our own files are prefixed `ours:`.

What this PR implements is the routing and dispatch half: (a), the quota awareness, and the fault-detector fix.
Everything from (c) to (j) is a finding and a recommendation, each sized as its own piece of work, so this PR stays reviewable and nothing is lost.

## (a) Dispatch profiles and LLM routing

**What First Mate does.**
Profiles live in one optional, gitignored `config/crew-dispatch.json` (`docs/configuration.md:416-467`); none ship, only `docs/examples/crew-dispatch.json` does.
A rule is a natural-language `when` plus an ordered `use` list of `{harness, model, effort}` candidates (`docs/configuration.md:424-458`).
There is no keyword classifier: the supervising model reads the `when` rules with judgment and passes concrete `--harness/--model/--effort` flags to `fm-spawn.sh` (`docs/configuration.md:417`; `AGENTS.md:219`).
An opt-in second path posts the whole brief to typesafe.ai and lets a paid third-party model pick the rule (`bin/fm-dispatch-resolve.sh:16-26`, `:224-238`).
Availability is read from `quota-axi --json`: a candidate is ineligible when any applicable scope has `runway.status == "exhausted_now"` or a known `effectivePercentRemaining <= 0`, unmeasured providers stay "eligible, unranked", and the winner is the highest `spendPriority` (`bin/fm-dispatch-resolve.sh:303-330`, `:372-374`).
Model strings are passed verbatim, no alias table (`bin/fm-spawn.sh:2163-2171`).
Explicit flags always win (`bin/fm-spawn.sh:2010-2030`), and a spawn without an explicit harness is refused while a profile file exists, so the rules are never silently skipped (`bin/fm-spawn.sh:1915-1918`).
There is no "routed vs explicit" line in the spawn output; provenance lives only in the model's rationale (`bin/fm-spawn.sh:4099-4105`).

**Taken into this PR, with the file it came from.**

- Unknown is not zero: a provider quota-axi cannot measure, or reports stale, is no evidence and never blocks (`bin/fm-dispatch-resolve.sh:315-328`; `.agents/skills/quota-array-dispatch/SKILL.md:114-117`). Ours: `internal/quota/quota.go` `Headroom`.
- A known zero percent counts as exhausted beside `exhausted_now` (`bin/fm-dispatch-resolve.sh:303-314`). Ours: `Headroom.Exhausted`.
- Every candidate is accounted for on the output, one finding per lane in the order tried (`bin/fm-dispatch-resolve.sh:395-402`). Ours: the `routed ... quota=deep: ...; build: ...` line.
- Model strings pass through untouched; no allowlist (`bin/fm-spawn.sh:2163-2171`). Ours: `routing.Lane.Model` goes straight to the harness.
- The consultation backstop, in spirit: routing is the default path rather than a flag, so a spawn cannot skip it by accident (`bin/fm-spawn.sh:1915-1918`). Ours: `cfo spawn` without `--harness` routes; `--auto` is an alias.

**Done differently, on the Overlord's direction.**
First Mate never trades reasoning class for quota; an exhausted preferred candidate escalates back to the model (`AGENTS.md:226`; `VISION.md:64`; `bin/fm-dispatch-resolve.sh:351-352`).
The Overlord asked for the next usable lane instead, disclosed on the spawn line, and a refusal with the reset time only when nothing is usable.
That is what ships, and the `routed` line names the wanted lane, the lane used, and the evidence, so the downgrade is never silent.
First Mate also has no staleness check in code (`.agents/skills/quota-array-dispatch/SKILL.md:43`); ours treats a snapshot older than an hour, or a provider quota-axi marks stale, as no evidence.

**Do not copy.**

- Rule matching through a paid third-party API with a hardcoded endpoint, plus a second model-judgment path (`bin/fm-dispatch-resolve.sh:17`, `:73-74`). Two classifiers to reason about; a keyword classifier over the ask is defensible and testable, and its ceiling is documented in `ours: internal/routing/execution.go`.
- Three hundred lines of jq re-parsing a TOON text envelope for a schema the tool also emits as JSON (`bin/fm-quota-choose.sh:123-307`).
- Two divergent harness-to-provider tables in one library (`bin/fm-quota-axi-lib.sh:96-137`).
- Record-and-omit effort, which drops a requested level from the launch while metadata says it was requested (`bin/fm-spawn.sh:2199-2208`; `docs/configuration.md:465`).
- Per-model quirks hardcoded in a codebase that says it has no model-specific policy (`bin/fm-spawn.sh:2188-2190` against `AGENTS.md:232`).

**Recommendation for the CFO.**
None beyond this PR.
The fleet table in `data/routing.json` plus `cfo doctor` covers the case; revisit only if a second provider family joins the fleet, at which point First Mate's per-rule `floor.min_percent` (`docs/configuration.md:443-458`) is the one field worth adding.

## (b) Per-harness supervision protocols

**What First Mate does.**
The protocol files under `docs/supervision-protocols/` describe how the primary session stays awake under each harness; worker-side differences live in `bin/fm-spawn.sh` `launch_template()` and `bin/fm-busy-lib.sh`.
Claude primary: two `Stop` hooks, the guard and an auto-arm with `asyncRewake: true, timeout: 28800` (`.claude/settings.json:38-52`); the model never re-arms (`docs/supervision-protocols/claude.md:6`).
Codex primary: a `Stop` hook exists (`.codex/hooks.json:31-40`) but there is no `asyncRewake`, so the replacement is a model-driven bounded foreground checkpoint of 180s (`docs/supervision-protocols/codex.md:7-16`).
Codex worker: launched with `--disable hooks` and `-c notify=[...]` touching the turn-ended file (`bin/fm-spawn.sh:1710-1715`), busy state deliberately unverified so a quiet Codex pane always surfaces as stale (`bin/fm-busy-lib.sh:88-90`).
Claude worker: a per-worktree `.claude/settings.local.json` with `UserPromptSubmit`, `Stop`, `StopFailure`, and `SessionEnd` hooks writing busy and turn-ended records (`bin/fm-spawn.sh:3708-3727`).
Rate limit and auth failures are not detected by the watcher at all; quota lives at dispatch and in an optional wake source (`bin/fm-quota-choose.sh:7-13`; `bin/fm-procevent-quota.sh:12-16`).

**Ours already has**: the Stop-hook pair with asyncRewake, the epoch ledger, the block budget, and an attended fail-open (`ours: cmd/cfo/hook.go:47-61`, `:176-191`, `:479-486`); PreToolUse arm, cd, and subagent guards (`:93-167`); goblin inertness through `CFO_ROLE=goblin` (`:37`; `ours: internal/harness/adapter.go:127-132`); same-harness resume where First Mate refuses (`ours: internal/harness/claude.go:71`, `codex.go:57`; `bin/fm-control.sh:184`); provider-fault routing where First Mate has nothing (`ours: internal/routing/routing.go`).

**Genuinely better in First Mate.**

- The Codex worker turn-end signal through `-c notify=` with the hook layer disabled (`bin/fm-spawn.sh:1710-1715`). Ours launches Codex bare (`ours: internal/harness/codex.go:35`), and `ours: internal/harness/adapter.go:26-27` admits `TurnEndedPath` is unused, so nothing writes `*.turn-ended` for any goblin even though `ours: internal/watch/watch.go:237` scans for it and `ours: internal/monitor/service.go:738` ages the busy reference off it.
- Poll-derived guard grace `max(300, FM_POLL+60)` (`docs/turnend-guard.md:66-67`). Ours is a flat 300s (`ours: cmd/cfo/hook.go:225`, `:488`) while `CFO_POLL` is free (`ours: internal/watch/watch.go:101`), so a poll of 300s or more makes a healthy watcher read stale.

**Do not copy.**

- The Codex primary checkpoint protocol (`docs/supervision-protocols/codex.md:7-9`): model-memory-owned re-arm that blocks the model 180s at a time, exactly what `docs/watcher-continuity.md:4` says continuity must not depend on. Our CFO runs under Claude only (`ours: AGENTS.md`, Supervision).
- Pane-hash staleness as the liveness backbone (`bin/fm-watch.sh:527-534`, `:1064-1066`). First Mate needs it because Codex busy state is unverified; we have Herdr `agent_status` and the CPU-delta rule, and pane polling is our recorded eighty-sweeps failure.
- Thirteen-harness shell sprawl and ancestry-walk identity (`bin/fm-harness.sh:136-155`; `bin/fm-spawn.sh` is 4532 lines). Our typed registry and `CFO_ROLE` stamp are the right shape.
- Cursor's synchronous 28800s park (`docs/turnend-guard.md:83`) and the away-mode daemon (`docs/wedge-alarm.md:3-4`), both cut from our build.

**Recommendation for the CFO.**
One dispatch-time piece of work, described under (i) below, since it is the same change.

## (c) Project instructions, agent memory, and context

**What First Mate does.**
One home holds `data/projects.md` (a thin registry, one markdown line per project, awk-parsed at `bin/fm-project-mode.sh:59-72`), `data/captain.md`, `data/learnings.md`, and per-task `data/<id>/brief.md` and `report.md` (`AGENTS.md:89-97`).
Project-intrinsic knowledge goes into the project's committed `AGENTS.md`, never written by the supervisor; a crewmate does it through `bin/fm-ensure-agents-md.sh:2-9`, which also injects a canonical "Maintaining this file" section (`:61-66`).
Session start is one script with nine ordered stages, fleet state before memory because harnesses truncate the tail (`bin/fm-session-start.sh:29-59`, `:86-99`), and a missing file prints `ABSENT` rather than nothing (`AGENTS.md:200-201`).
Memory entries carry trailing markers: aging (stale at 30 days), perishable (7 days, must name a checkable expiry), pinned (`.agents/skills/stow/SKILL.md:21-26`, `:37-40`); stale never means deleted, it moves to `data/memory-archive.md` with provenance (`:132-137`).
A startup memory budget of 7,500 estimated tokens bounds the always-injected files (`bin/fm-startup-memory-budget-lib.sh:12`, `:151-161`).

**Ours.**
`data/projects/<name>/project.json` is a typed schema with unknown fields refused (`ours: internal/project/manifest.go:86-106`), but on this machine no project has one; they hold `auth.json` and, for three, `worktree.json`.
The supervisor memory the session instructions name is Claude-Code-native, has no tiers, dates, budget, or archive, and is invisible to a codex, kimi, or pi goblin and to a CFO run under any other harness.

**Genuinely better**: the tiered decay with a cold archive and a byte budget (`.agents/skills/stow/SKILL.md`), the `ABSENT` semantics and fleet-before-memory ordering at session start (`bin/fm-session-start.sh:86-99`), and one owner for project memory (`bin/fm-ensure-agents-md.sh`).

**Do not copy**: the awk-parsed markdown registry (`bin/fm-project-mode.sh:59-72`) against our typed JSON, and the secondmate cascade (`bin/fm-stow-cascade.sh:2-7`), because we have no secondmates.

**Recommendation (its own piece of work).**
Move supervisor memory into the fleet data directory as `data/learnings.md` plus `data/memory-archive.md`, add a `cfo memory report` that estimates tokens as `ceil(bytes/3)` and is printed by `cfo doctor`, and ship a `stow` skill in `.agents/skills/` carrying the aging and perishable markers.
Memory becomes harness-independent and self-pruning, and the empty `project.json` schema can be cut down to what the fleet actually reads.

## (d) Worktree model

**What First Mate does.**
A treehouse-managed pool outside the repo, fixed `<pool>/<slot>/<repo>` layout with `treehouse-state.json` at the root (`bin/fm-wake-lib.sh:1235-1244`).
The worker pane itself types `treehouse get`, and spawn polls the pane's working directory for up to 60s (`bin/fm-spawn.sh:3489`, `:3501-3546`).
Slots are numbered, not named, so task identity needs a sibling `.fm-slot-owner` file and a `worktree=` record (`bin/fm-wake-lib.sh:1270-1299`); the comment at `:1257-1268` explains the claim exists because a lapsed lease reads identical whether the slot is still this task's or was handed to another.
Provisioning is git only: fetch, reset to the remote default tip, refuse a non-clean slot (`bin/fm-spawn.sh:204-211`); no dependency, env, or config provisioning exists anywhere in `bin/`.
Teardown refuses unless work has landed: reachable from a remote-tracking branch, or a merged PR head contains the local work, or the content is in the up-to-date default branch; uncommitted work is never landed and an inconclusive check refuses (`bin/fm-teardown.sh:44-68`).
A stale `index.lock` is removed only when provably stale by `lsof` and mtime (`bin/fm-lock-lib.sh:4-15`; `bin/fm-teardown.sh:179-200`).

**Ours.**
In-repo `<project>/.worktrees/gb-<id>`, detached on the origin default, the path itself the lease under the spawn lock (`ours: internal/worktree/git.go:27-53`; `ours: internal/spawn/spawn.go:142`).
Provisioning is real: linked `.env*`, dependencies against shared caches, a filtered `.mcp.json` (`ours: internal/worktree/provision.go:60-67`).
Return refuses only a dirty status (`ours: internal/worktree/git.go:204-213`); committed-but-unpushed work on the detached head is destroyed by `worktree remove --force`, which is the "worktree deliverables die in cleanup" note in supervisor memory.

**Genuinely better**: the landed-work proof before teardown (`bin/fm-teardown.sh:44-68`) and the stale-lock proof (`bin/fm-lock-lib.sh`).

**Do not copy**: pooled slot reuse with pane-driven allocation and cwd polling (`bin/fm-spawn.sh:3489-3546`); the entire `.fm-slot-owner` apparatus exists only because reuse makes `worktree=` records go stale, and our path-is-identity model removes that class of bug. Skip the external `treehouse` dependency and its version floor (`bin/fm-bootstrap.sh:928-929`).

**Recommendation (its own piece of work).**
Extend `cfo cleanup` and `worktree.Return` with a landed proof: refuse when the worktree head is not reachable from any `refs/remotes/*` ref, or from the fetched default branch, or when the recorded PR is not merged with a head containing the local head; refuse on inconclusive; add an explicit `--discard` that requires the Overlord's word.
That mirrors `bin/fm-teardown.sh:44-68` without the slot machinery and closes the unpushed-work hole.

## (e) Review-surface skill wiring

**What First Mate does.**
No skill directory for it; the tool is third-party `lavish-axi` with a version floor and a `PRESENTATION_UNAVAILABLE` text fallback (`bin/fm-bootstrap.sh:881`, `:926`, `:1485-1486`).
The blocking poll is never run in a turn: `bin/fm-procevent-lavish.sh arm <artifact.html>` registers the poll as a supervised background source (`:150-160`), the generic runner captures output durably and publishes ordinary `check` wakes (`bin/fm-procevent.sh:37-49`), an empty board close is `silent` and an ended session is `terminal` (`bin/fm-procevent-lavish.sh:37-52`).
The bearings board is a stable file proven live before arming (`bin/fm-bearings-board.sh:17-24`, `:38-47`).
Only scout briefs get a Lavish line (`bin/fm-brief.sh:362-366`); ship briefs say nothing.
Discovery is a `.claude/skills -> ../.agents/skills` symlink (`AGENTS.md:66-67`); there is no `.codex/skills` or `.pi/skills`.

**Ours.**
`showcase` at `.agents/skills/showcase/SKILL.md`, foreground poll only (`:29-34`, `:36-44`), session state beside the artifact with delivered items marked so restarts never repeat (`ours: internal/showcase/session.go:18-20`, `poll.go:10-13`).
Junctions for `.claude/skills` and `.codex/skills` are created by `install.ps1:59-62`, and a fresh worktree has no `.claude/` until bootstrap runs, so a goblin cannot discover the skill.
Nothing in `cmd/cfo` or the watcher knows the tool exists: the CFO's turn blocks on `showcase-axi poll`, and goblins are never told.

**Genuinely better**: the background source with durable capture, silent empty close, and terminal retirement (`bin/fm-procevent.sh`, `bin/fm-procevent-lavish.sh`), and prove-live-before-arm (`bin/fm-bearings-board.sh:38-47`).

**Do not copy**: the twelve-times retry on one server error string (`bin/fm-procevent-lavish.sh:90-105`), the board payload validator and template (`:66-73`), and the destructive-poll semantics; our delivered marking is already stronger.

**Recommendation (its own piece of work).**
Register `showcase-axi poll <file>` as a watcher-supervised background source whose delivered payload becomes a CFO wake, with an empty user close recorded silently.
That removes the foreground-poll rule from the skill and makes the surface usable for goblin deliverables as well.

## (f) Context management

**What First Mate does.**
No context-token thresholds exist anywhere; looked in `AGENTS.md`, `docs/calm.md`, `docs/calm-mode-feasibility.md`, `docs/watcher-continuity.md`, `docs/sessionstart-nudge.md`, `bin/`, `.agents/skills/`.
The only token number is the startup memory budget (`bin/fm-startup-memory-budget-lib.sh:12`, `:151-161`), enforced by `/stow` refusing to end a pass over budget (`.agents/skills/stow/SKILL.md:118-127`).
Compaction is treated as "the digest was lost": `clear` and `compact` hook sources re-emit the session start, which re-presents the wake queue and skips the mutating sweeps (`docs/sessionstart-nudge.md:27`, `:32`; `bin/fm-session-start.sh:194-204`).
A crewmate nearing its limit is not detected by design: "A low context reading is not wedging; modern harnesses auto-compact and keep going" (`.agents/skills/stuck-crewmate-recovery/SKILL.md:72`).
Restart, not compaction, is the continuity primitive (`AGENTS.md:251`), and the captain is never told about context (`AGENTS.md:479`).

**Ours.**
`ours: internal/project/manifest.go:69-74` declares `warn_context_tokens`, `compact_context_tokens`, and `restart_context_tokens`; nothing reads them.
`ours: cmd/cfo/hook.go:635-660` already routes `clear` and `compact` to a full digest, and `ours: internal/digest/digest.go:350-357` prints `learnings.md` in full with no bound.
Our real context problem is the recorded eighty-poll burn, not a threshold.

**Do not copy**: the five-harness session-source matrix (`docs/sessionstart-nudge.md:70-83`), the Calm mod (`docs/calm.md:66-70`, presentation only, zero context benefit), and a budget number without the curation loop that enforces it, which is what our Budget struct already is.

**Recommendation (its own piece of work).**
Delete the three unread `*_context_tokens` fields and put the one number First Mate enforces where our digest can act on it: an estimated-token cap on the digest's context section using `ceil(bytes/3)`, printing an over-budget banner instead of the full file, mirroring `bin/fm-startup-memory-budget.sh:66-71`.

## (g) Questions

**What First Mate does.**
A crewmate asks with one status line and stops: `needs-decision: {options}`, `blocked: {why}`, `paused:` for a known wait (`bin/fm-brief.sh:393-403`; verbs at `bin/fm-classify-lib.sh:78-90`).
Questions are keyed, and only a keyed answer closes them: "a later `done:` or `working:` line never closes it" (`bin/fm-brief.sh:402-403`); the answer itself writes the close through `bin/fm-send.sh <task> --resolve-key <key> '<answer>'` (`bin/fm-send.sh:4`; `AGENTS.md:332`).
Every drain prints fleet-wide `OPEN DECISIONS` even on an empty queue (`bin/fm-wake-drain.sh:479-488`); bearings buckets holds as blocked, dated, aged (14 days), or live (`docs/captain-hold-lifecycle.md:126-130`).
Nothing is dropped, by five mechanisms: rows survive until a generation-bound ack (`bin/fm-wake-drain.sh:860`), declared waits re-surface on a timer (`bin/fm-watch.sh:291`), the steer inbox re-rings every 90s and escalates after three (`bin/fm-task-inbox-lib.sh:47-53`), secondmate replies carry a pending record that never silently expires (`bin/fm-pending-reply-lib.sh:11-17`), and parent-channel facts are written by scripts rather than remembered (`docs/secondmate-parent-channel.md:16-21`).

**Ours.**
Same three verbs (`ours: cmd/cfo/notify.go:18-20`).
`ours: cmd/cfo/drain.go:46-66` refuses an ack while blocked or failed rows sit at or below the sequence, and `--ack-blocking` is range-scoped: there is no way to retire a later question while keeping an earlier one (`:63`).
We have no keyed resolve, no "later", no age bucket, no re-surface timer, and `cfo send` closes nothing, so the only exit for a question is the range ack.

**Do not copy**: the backlog as a second decision store with a 1930-line hold script and reconcile requests (`docs/captain-hold-lifecycle.md:178-189`), `RECORD DIVERGENCE` (`:161-168`), which exists only because there are two records of one call, and the 1563-line pending-reply ladder (`bin/fm-pending-reply-lib.sh`), since secondmates are cut from our build.

**Recommendation (its own piece of work).**
Give `cfo send` a `--resolve <seq>` that retires exactly the blocked or failed record the answer answers, the shape of `fm-send --resolve-key` (`bin/fm-send.sh:4`), and make `cfo drain` print an `OPEN QUESTIONS` section with age, sequence, and question on every drain regardless of the ack floor (`bin/fm-wake-drain.sh:479-488`).
Then delete range-scoped `--ack-blocking`.

## (h) Verification

**What First Mate does.**
Done is mode-specific with one owner (`bin/fm-dod-lib.sh:2-12`): direct-PR ends with `done: PR {url}` (`:244`), no-mistakes ends with `done: PR {url} checks green` only after CI is green, and `--yes` is banned fleet-wide (`:289-292`).
The brief carries a machine-readable `Delivery contract: mode=` line and spawn refuses a mismatch (`:11-12`; `docs/architecture.md:321-322`).
Merge reads live state and pins the head: open, not draft, mergeable, every check green at the current head, `--match-head-commit`, then a post-merge read-back that must show merged or queued (`bin/fm-pr-merge.sh:11-31`).
Work has landed only when reachable from a remote-tracking branch, or its merged PR head contains the local work, or its content is in the default branch; inconclusive refuses (`bin/fm-teardown.sh:44-68`).
`docs/verification/*.md` are dated empirical records with exact commands and outputs, and each design doc names its regression entry points (`docs/watcher-continuity.md:113-124`).

**Ours.**
We already pin `--match-head-commit` (`ours: cmd/cfo/deliver.go:223`) and refuse red or unverified PRs (`ours: cmd/cfo/pr_proof.go:61-84`).
They verify what we do not: the landed-work test before cleanup, a post-merge read-back, a spawn-refused mode line, and post-merge contribution watching.
We verify what they do not: review decisions `CHANGES_REQUESTED` and `REVIEW_REQUIRED` (`ours: cmd/cfo/pr_proof.go:73-78`), a durable repair-cycle budget that never becomes approval (`ours: docs/pipeline.md:29-34`), a frozen per-task policy snapshot with SHA (`:16-17`), and reviewer drift refused before start (`:93`).

**Do not copy**: the GitLab path (`docs/gitlab-merge-watch.md`), byte-static poll shims as a shell-injection defence for a shell watcher (`AGENTS.md:113-117`), and the attended `--allow-red <check>` waiver (`bin/fm-pr-merge.sh:21-25`); our `cfo pr merge` never merges red, and that stays absolute.

**Recommendation (its own piece of work).**
Port the landed test into `cfo cleanup` (`ours: cmd/cfo/cleanup.go:18-21`), refusing unless the branch head is reachable from a remote-tracking ref, or the recorded PR is merged with a head containing the local head, or the content is already in the default branch, and refusing on inconclusive rather than falling back to "not dirty".
This is the same change as (d) and should ship once.

## (i) Hooks

**What First Mate does, and what #4689 fixed.**
Commit `fa93097` ("launch codex crewmates with codex's hook layer disabled", #4689) changed the Codex crewmate launch to `codex ... --dangerously-bypass-approvals-and-sandbox --disable hooks -c "notify=[\"bash\",\"-c\",\"touch __TURNEND__\"]"` (`bin/fm-spawn.sh:1710-1715`).
The comment above it (`:1688-1709`) says why: without `--disable hooks` a crewmate launch parked forever on Codex's hook-trust modal ("N hooks are new or changed"), whose selection sits on "Review hooks" and cannot be moved through First Mate's key plane; pre-accepting it by writing Codex's own trust store would manufacture operator consent.
The hooks it asks about are the operator's machine-level `~/.codex/hooks.json` and any project-local `.codex/hooks.json`, and a crewmate needs none of them: its turn-end signal is the `-c notify=` program on the same launch, verified still firing with hooks disabled on codex-cli 0.151.0.
A secondmate is a primary in its own home, so its launch deliberately keeps hooks on, because its turn-end guard and seatbelts are exactly those project hooks.
Claude's Stop hook and Codex's Stop hook differ in one field: Claude marks every stop after any stop-hook-driven continuation `stop_hook_active=true`, including asyncRewake rewakes, which re-opened a blind window in 2026-07, so the guard runs with `--claude` and ignores the field; default Codex mode lets a true value finish the second stop after one forced continuation (`docs/turnend-guard.md:92-97`; `bin/fm-turnend-guard.sh:60-84`).
Codex has no `asyncRewake`, so its primary runs bounded foreground checkpoints instead (`docs/supervision-protocols/codex.md:7-16`).
Claude workers get a per-worktree `.claude/settings.local.json` with `UserPromptSubmit`, `Stop`, `StopFailure`, and `SessionEnd` hooks writing busy and turn-ended records (`bin/fm-spawn.sh:3708-3727`).

**Ours.**
`ours: cmd/cfo/hook.go` already ignores `stop_hook_active` (`:206-209`), keeps the sync wait, block budget, and epoch ledger (`:225-228`, `:479-486`), and adds a hard escalation ceiling First Mate lacks (`:288-298`).
Goblins are made inert through `CFO_ROLE=goblin` (`:37`), which does for Claude what `--disable hooks` does for Codex.
But `ours: internal/harness/codex.go:35` launches Codex bare, so a Codex goblin on a machine with a `~/.codex/hooks.json` can hit the same trust modal First Mate hit, and no goblin of any harness writes `*.turn-ended`.

**What our hooks and `cfo hook` entry points should adopt.**

- At dispatch, launch Codex goblins with `--disable hooks` and `-c notify=["cfo","hook","turn-ended","<id>"]`, writing `state/<id>.turn-ended` through our own binary rather than a bash `touch` (Windows has no `bash -c touch` to rely on); keep hooks on only for a primary, which we never run under Codex. Source: `bin/fm-spawn.sh:1688-1715`.
- At dispatch, give Claude goblins a per-worktree `.claude/settings.local.json` whose `Stop`, `StopFailure`, and `SessionEnd` hooks call the same `cfo hook turn-ended <id>`, so the watcher's `*.turn-ended` scan (`ours: internal/watch/watch.go:237`) and the monitor's busy ageing (`ours: internal/monitor/service.go:738`) finally have a signal on both harnesses. Source: `bin/fm-spawn.sh:3708-3727`.
- In the guard, derive grace as `max(300, CFO_POLL+60)` rather than a flat 300 (`docs/turnend-guard.md:66-67`; `ours: cmd/cfo/hook.go:225`, `:488`).
- On relaunch and `cfo switch`, retire the old harness's hook wiring before installing the new one, the shape of `clear_relaunch_harness_wiring` (`bin/fm-spawn.sh:3644-3653`).

**Do not copy**: the Codex primary checkpoint protocol, trusting `stop_hook_active` for Claude (First Mate itself regressed on it, `docs/turnend-guard.md:97`), gating host detection on env (`bin/fm-hook-host-lib.sh:14-21` gates on the payload, which is right, but only matters with a second primary harness), and the away-mode daemon.

**Recommendation (its own piece of work).**
One PR: the four bullets above, with a live check on the installed codex that `--disable hooks` is still an accepted flag (an unknown feature name is a hard Codex error, which First Mate relies on so a future release fails loudly, `bin/fm-spawn.sh:1704-1707`).

## (j) Session contribution back to the user

**What First Mate does.**
`/stow` sweeps preferences, project facts, gotchas, standing decisions, and undone next steps (`skills/stow/SKILL.md:18-24`), plus open-record persistence: file open work never filed, correct records now stale, bounded to what the session holds (`.agents/skills/stow/SKILL.md:237-245`).
Routing table for where each kind goes (`AGENTS.md:268-275`): `data/captain.md`, `data/captain-shared.md`, `data/learnings.md`, a backlog item, a scout report, a project `AGENTS.md` only through a crewmate ship task, or a PR to shared tracked material.
Decay markers and reinforcement rules (`.agents/skills/stow/SKILL.md:21-26`, `:37-41`, `:99-100`): decay advances only when a pass runs, and re-reading memory is never reinforcement.
Over budget, a fixed order applies: archive stale, consolidate, offload conditional entries to an existing just-in-time owner, evict oldest-first only if the pool can reach budget; pinned is never auto-moved; a remaining excess becomes a captain-held decision, never an accepted exception (`:112-127`).
Duplicate protection is inspect-then-update with six graduation moves (`:230-235`), and HEAD `3eb5b63` makes a merged or closed contribution observation final so it is never re-read (`bin/fm-contributions.sh:43-46`).
At session start the digest prints the three memory files in full with `ABSENT` marked (`AGENTS.md:200-201`); `/bearings` renders four fixed sections from one deterministic snapshot (`.agents/skills/bearings/SKILL.md:16`, `:40-43`); `/ahoy` is session-history-only and falls back to bearings when it is the first captain message (`.agents/skills/ahoy/SKILL.md:11`, `:30`).
A pass ends with a receipt naming one action per file and may claim "reset-safe" only when within budget with no exception (`.agents/skills/stow/SKILL.md:260-273`).

**Ours.**
The digest prints `projects.md`, `overlord.md`, and `learnings.md` (`ours: internal/digest/digest.go:350-357`), unbounded and untiered, and nothing sweeps a session into them.
The supervisor's memory index has provenance per fact but no decay clock, no budget, and no archive tier.

**Do not copy**: the secondmate cascade (`.agents/skills/stow/SKILL.md:277-302`; `bin/fm-stow-cascade.sh`), the user-owned local-skill offload with git-exclude bookkeeping (`:175-181`, `:200-210`), and the pass-horizon counter (`:57-75`); date clocks suffice.

**Recommendation (its own piece of work, and the CFO's end-of-session rule until it ships).**
Add a `/stow` equivalent to the CFO contract with three required outputs, copied from `.agents/skills/stow/SKILL.md:260-273`: (1) every unfiled open question or blocked goblin filed as a wake record or a memory fact; (2) each memory fact written inspect-then-update with a `last_reinforced` date in its frontmatter, so a later pass archives at 30 days instead of deleting; (3) a one-line resume pointer and an explicit "reset-safe: yes/no" verdict, which may not be "yes" while any `--blocked` notify is unanswered in `cfo drain`.

## Summary of what not to copy

- typesafe.ai rule resolution and the jq TOON re-parser (`bin/fm-dispatch-resolve.sh`, `bin/fm-quota-choose.sh`).
- The Codex primary foreground checkpoint protocol (`docs/supervision-protocols/codex.md`).
- Pane-hash liveness (`bin/fm-watch.sh`) and trusting `stop_hook_active` for Claude (`docs/turnend-guard.md:97`).
- Pooled worktree slots, pane-driven allocation, and the `treehouse` dependency (`bin/fm-spawn.sh:3489-3546`, `bin/fm-wake-lib.sh:1257-1299`).
- The awk-parsed project registry (`bin/fm-project-mode.sh`) and every secondmate cascade or pending-reply ladder.
- The backlog as a second decision store (`bin/fm-captain-hold.sh`) and `RECORD DIVERGENCE`.
- The `--allow-red` merge waiver (`bin/fm-pr-merge.sh:21-25`) and the GitLab path.
- The Calm mod, the away-mode daemon, Cursor's 28800s park, and the record-and-omit effort translation.

## Pieces of work this review proposes, in priority order

1. Codex and Claude goblin turn-end wiring at dispatch, with `--disable hooks` for Codex and poll-derived guard grace; (b) and (i).
2. Landed-work proof in `cfo cleanup`; (d) and (h), one change.
3. Keyed question resolution and an always-printed `OPEN QUESTIONS` section; (g).
4. Fleet-resident supervisor memory with decay, budget, archive, and an end-of-session stow rule; (c), (f), and (j).
5. Showcase poll as a watcher-supervised background source; (e).
