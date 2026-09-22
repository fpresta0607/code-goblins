# CFO Native Supervisor Implementation Plan

**Goal:** Deliver the approved native supervisor and live SIQstack-styled task board, including the reproduced Windows launch regression.

**Architecture:** Extend the existing Go control plane with a persistent supervisor that owns the watch singleton, ingests atomic hook events, evaluates task evidence, and journals actions.
The React board consumes snapshots and an event stream from its loopback HTTP API.
Lifecycle signals request evaluation and never establish completion on their own.

**Tech stack:** Go, existing Herdr and no-mistakes contracts, React, TypeScript, Vite, and Windows filesystem notifications.

Implementation is already authorized.
The assigned worker implements in this worktree without dispatching another CFO.
The live worker uses gpt-6-astra with max effort.

## Execution and verification

1. Add failing behavioral regressions for typed launch prompt delivery and Codex max effort in `internal/harness` and `internal/spawn`.
   Remove prompts from PowerShell native arguments and submit them through the existing verified Herdr prompt path.
2. Implement typed native events, bounded atomic spooling, normalization, capability checks, and idempotent installation in `internal/nativehook` and `cmd/cfo`.
   Verify Claude Code, Codex, and Pi payloads and isolated configuration installation.
   Preserve unrelated settings and Codex hook trust review.
3. Add persistent supervision in `internal/supervisor`, using existing monitor, watch, wake, state, and supervision contracts.
   Test replay, malformed and stale events, task/session identity, restart, and interrupted action handling.
   Recover safe evaluations and park ambiguous external actions for inspection.
4. Expose a loopback API and stream, bounded Git details/history, contextual feedback, and on-demand Herdr terminal controls.
   Test origin/host checks, unsafe paths/revisions, action idempotency, missing tools, and evidence-backed progression.
5. Adapt the pinned Cline Kanban board/diff/history/stream source with Apache attribution and modification notices.
   Build a responsive React board using the inspected SIQshift brand stylesheet and controls.
   Embed Vite output in Go and verify all enabled controls against the backend.
6. Run bounded Go tests/vet, frontend type/lint/build checks, and Windows binary integration checks.
   Exercise browser selection, details, feedback, reconnect, narrow layouts, restart, and browser-closed progression.
   Measure attributable Windows CPU and memory and preserve concise evidence.
7. Commit the final changes and run the frozen CFO no-mistakes gate.
   Preserve operator configuration even if the frozen v1 policy refuses the current codex/claude primary chain.
   Record and publish a reviewable PR only through the authorized gate path, report through `cfo notify cfo-native-board`, and retain explicit merge authority.

## Safety requirements

Before any test step invokes `cfo` or `go test`, clear `CFO_HOME` and `CFO_STATE_OVERRIDE` or point both at an isolated temporary home.
Include that exact requirement in the no-mistakes intent.
Never run synthetic events, install/uninstall, or test mutations against the live fleet or actual user harness settings.
Do not modify the primary checkout's operator edits or shared machine gate policy.
Keep test/build concurrency bounded because machine memory headroom is limited.

## Source findings

- Installed contracts inspected on 2026-09-22: Codex 0.154.0, Claude Code 2.1.278, Pi 0.85.1.
- Codex documentation: <https://learn.chatgpt.com/docs/hooks>.
  User `hooks.json` and inline configuration coexist, and exact hook definitions require trust review.
  `Stop` requests evaluation while `SessionEnd` records the end of the main session.
- Pi documentation: <https://pi.dev/docs/latest/extensions>.
  Settled notification follows automatic continuations and retries.
- Claude Code documentation: <https://code.claude.com/docs/en/hooks>.
- Cline Kanban revision: `abd4912c27ce6b7f18b5a8106c145fd838e90cc4`.
  The root LICENSE is Apache-2.0, Copyright 2026 Cline Bot Inc.; the root NOTICE path returns 404.
- SIQshift's brand and spool helper were inspected read-only.
  Its existing Codex registry is reference material, not evidence that CFO hooks are installed.
- This checkout has no `internal/backend` package; existing backend behavior lives in the Herdr client and command runtime.

## Progress

- Requirements, current architecture, installed harnesses, upstream references, and brand inspected.
- Launch regression complete: Codex accepts max effort and fresh/resumed prompts use verified Herdr delivery instead of PowerShell native argument quoting.
- Hook contract normalization and isolated idempotent setup tests pass.
- Persistence fault checkpoint passes: memory rolls back on failed atomic writes, retryable inbox events survive, and a 301-record hash-named backlog is ordered before batching.
- Git preview/path safety and exact-head gate evidence checks pass.
- Supervisor/API implementation and the full Go suite pass, including persistence rollback/retry, chronological backlog ingestion, rollover, custody, exact-HEAD evidence, landed-content verification, and same-turn Stop continuation regressions.
- Browser-closed physical harness crash, supervisor restart, and automatic browser reconnect pass against an isolated real Herdr session.
- React type/lint/build and five behavior tests pass; clean lockfile installation regenerates byte-identical embedded assets.
- CI and release now check the frontend and verify regenerated embedded assets before Go compilation.
- Desktop and 390 px board/diff/lineage/details screenshots were inspected by the CFO and accepted as coherent/readable.
- A lost HTTP response followed by SSE success produced exactly one native Herdr receipt and no UI retry dispatch.
- Child nodes with no native model show Model unreported and never inherit the goblin's model or effort.
- Attributable supervisor/Herdr/browser/fixture CPU and memory samples plus direct/native PowerShell hook latency are recorded in the verification report.
- Earlier Defender quarantines remain unresolved; targeted intermediate scans recorded no detections for their exact artifacts.
- Precommit smoke artifact built and targeted Defender scan 99DE9B93-784D-4AF3-8C3E-7B4CD76DDF82 completed with no detection for that path; earlier quarantines remain unexplained.
- Final restart regression passed ten times, then the full supervisor suite and vet/module verification passed.
- Commit these verified sources/evidence, build and scan outside the clean worktree with vcs.modified=false, and record the final manifest in task data before invoking managed no-mistakes.
- Operator policy and merge authority stay unchanged; gate outcome is recorded in external task status without dirtying the delivery commit.

## Approved UI and lineage extension

The CodeBlock and workflow canvas attachments are visual references for the React board.
Use syntax-highlighted unified/split diffs, numbered lines, file selection, a code preview, and contextual feedback.
Provide selectable collapsible lineage nodes plus a compact nested list and a details drawer.
Persist only native or runtime-reported parent/root relationships, keeping unlinked sessions explicit.
Handle subagent lifecycle, replays, stale data, attempted reparenting, and cycles without inventing parentage.
Do not introduce Tauri or node creation controls.
Distinguish reported delegation/spawn links from task dependencies.
