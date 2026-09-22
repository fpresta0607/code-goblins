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

The CodeBlock and workflow canvas attachments were visual references for the first React board.
Use syntax-highlighted unified/split diffs, numbered lines, file selection, a code preview, and contextual feedback.
The original selectable lineage/list and tabbed details presentation is superseded by the UI simplification below.
Persist only native or runtime-reported parent/root relationships, keeping unlinked sessions explicit.
Handle subagent lifecycle, replays, stale data, attempted reparenting, and cycles without inventing parentage.
Do not introduce Tauri or node creation controls.
Distinguish reported delegation/spawn links from task dependencies.

## Approved UI simplification, 2026-09-22

The revised brief authorizes implementation without another design gate.
Use one dark/mint workflow canvas with a compact top bar, generous whitespace, approximately 22 px node titles and 16 px body text.
Show only name, role and status on node faces; put model, runtime and freshness in one contextual panel.
Render explicit parent-child relationships with simple connectors, retain unknown/retired/cyclic evidence, and show tasks lacking their owning session as standalone nodes.
Keep task dependencies distinct in the task context and unmatched decisions in a small disclosure on the canvas.
Automatically flow vertically on smaller canvases; never offer a second layout or main-view tabs.
Keep the canvas visible beside the desktop panel and use the full width for the selected panel on narrow screens.

1. Replace `App.tsx`, `Lineage.tsx` and obsolete `Board.tsx` presentation with the single canvas; retain and test lineage and model attribution in `lineageTree.ts`.
2. Refactor `Details.tsx` into a nonmodal desktop panel with readable summary, lazy disclosure sections, compact file selection and task decisions/dependencies.
   Keep feedback request identity alive while its disclosure closes and reopens.
3. Replace the diff-format buttons in `DiffView.tsx` with a select and replace `styles.css` instead of layering overrides over the removed views.
4. Run isolated frontend typecheck/lint/behavior tests/build and relevant embedded-asset checks.
   Verify desktop/narrow keyboard selection, close/focus, collapse, zoom, wider/deeper/long-name trees, missing identities, decisions, code/history/terminal, feedback and lost-response/SSE success, plus live reconnect and empty/error states in a task-owned browser tab.
5. Record fresh screenshots and outcomes, commit the source and regenerated assets, then retain the blocked managed gate without retrying unchanged policy.
   If producing a revised executable, build from that exact clean commit in a temporary checkout outside the parent repository and scan that exact file before execution.
   Preserve the earlier unexplained Defender quarantines and the original artifact/report.

### Revision milestones

- Read the revised brief and current UI, confirmed clean source commit `9c9af11`, and recorded this authorized design.
- Shared gate policy, credentials, merge authority and existing artifacts remain outside this UI revision.
- Single canvas and SIQshift rounded controls/disclosures are implemented; frontend typecheck and lint passed before the diff-review extension.
- The literal credential scan reported zero in scanned worktree/commit/artifact locations and five matches in the existing worker transcript; remaining logs were not scanned after that first match.
  Keep that precise evidence and do not certify a broader absence.

### Codex-style review extension

The latest brief supersedes the primary file selector with stacked, individually disclosed file diffs.
Open Changes prominently and give the selected task review panel more width; keep secondary runtime facts in Session evidence.
Support old/new line selection and contiguous ranges with an inline comment and explicit Send to CFO action.
Add an independent durable `review` action using the existing wake queue and recovery episode; never route review comments through worker steering.
Validate task generation, path, revision, HEAD, diff fingerprint and all selected coordinates on the server, and derive bounded selected code there.
Review may reach the CFO during pipeline custody, but existing direct steering keeps its custody checks.
Persist request identity in the selected task panel across file/Changes disclosure toggles, ambiguous HTTP results and SSE outcomes.
Test two-file queue delivery with no Send callback, invalid/stale ranges, request conflicts, restart and uncertain delivery; verify the same path in the browser.
For capped deep-tree indentation, name the actual reported parent so five-level descendants cannot look like unexplained siblings.
Shared gate mutations belong to the owning CFO pending coordination.

### Review verification milestones

- Added independent durable CFO review actions with bounded old/new ranges, including visible unchanged context, and exact task/revision validation.
  Two-file queue, no worker steering, stale task/HEAD, conflicting request IDs and interrupted delivery regressions pass.
- Frontend typecheck, lint, seven behavior tests and production build pass.
  Drafts bind the selected task session identity and preserve request IDs across disclosure toggles; legacy direct-feedback line context is removed.
- Full supervisor and wake suites pass in isolated state (70.491 s and 1.545 s).
- Isolated browser preview is `http://127.0.0.1:58099` with a deterministic harness and independent CFO queue.
  Its exact executable hash `3BDF30BE58241F731511892CF52508F9CE84656C5F8278B59E16FA91F19F114A` has a completed targeted Defender scan, also verified by the CFO.
  Desktop canvas screenshot: `C:\dev\code-goblins\data\cfo-native-board\ui-canvas-desktop.png`.
  Browser interaction and exactly-once review checks are in progress in a task-owned tab.
- Real browser review delivered `supervisor.ts` and `board.css` new-side lines 2-3 as two distinct CFO wake records with exact HEAD/fingerprint and server-derived code.
  No worker receipt file exists, and no direct feedback was sent.
  The first response was deliberately lost after server acceptance while Changes was closed; reopening displayed SSE success with one POST and a disabled duplicate send.
  The first reopen assertion ran before the lazy preview finished; checking after load confirmed the draft and receipt were retained.
  Exact fixture evidence is retained in `C:\dev\code-goblins\data\cfo-native-board\ui-review-delivery.json`.
- Native queue feedback exposed an overly verbose raw review record in task decisions.
  The source now discloses concise comment/range text and keeps the waiting item compact; frontend checks are running.
- The user subsequently requested Board and Orchestration views and a GPT image mockup before the next visual revision.
  The earlier one-canvas visual constraint is superseded.
  Preserve this checkpoint and wait for the parent-provided mockup and updated brief before implementing that design.

### Approved generated design implementation

The user approved the generated mockup and authorized recreation, superseding the preceding pause.
The production app icon and six-persona transparent atlas have been inspected and copied without editing into public assets.
Render one of Board or Orchestration at a time, with a persistent contextual right pane and no column add controls.
Board groups honest task states into Tasks, In progress and Completed.
Orchestration uses actual lineage in a draggable, zoomable spatial canvas; positions are presentation state and cannot reparent workers.
Use task-semantic avatars with a stable general default and expire communication pulses unless actual fresh targeted delivery supports them.
The primary CFO conversation needs a native registered identity bound at queue and execution time, durable requests and no wrong-pane fallback.
Active tasks open captured terminal/verified input; completed tasks open existing multi-file review.
Keep review drafts and request identities across view/pane changes.
Validate real CFO delivery and primary replacement, drag/keyboard/zoom, task selection defaults, pulse expiry and range review before final screenshots and commit.
Shared gate policy remains held for the owning CFO.
- Two-view implementation now includes semantic goblin atlases, active-task native terminal input, completed-task Changes, a persistent CFO pane, and App-owned review/message drafts.
  Frontend typecheck, lint, 10 behavior tests and production build pass at this checkpoint.
- Full Go suite passed before the latest native conversation changes; focused CFO identity, full-history rejection and bounded capture tests pass (6.941 s).
  Primary validation precedes history reclamation, and shared CFO/worker capture requests have a one-slot, eight-second bound.
- The previous example's orphan alert came from watch.ConfigFromEnv constructing the production machine-wide CIM/reap inventory alongside an isolated Herdr session.
  The explicit temporary-home-only --example mode now labels Example workspace and omits that machine-wide inventory, while retaining native task monitoring and custody checks.
  No shared process was cleaned up and no operational wake record was acknowledged.
- Intermediate UI executable: C:\Users\fpres\AppData\Local\Temp\cfo-native-board-ui-v3.exe, SHA256 6E1B2F238BE414EFDB21C7C92B3B60E85A1E871012686187FE9F0128FEB7A7FC.
  Targeted Defender scan C7CB72BB-3DA0-47D2-A69D-6B9BAAE07645 completed at 17:57:09 Central with zero exact-path detections.
  The separate deterministic harness SHA256 D7E8866BEF3A304677DD4656BDD3B3084D22510035E4E647C16C1E3329B34CC7 completed scan 999677F3-853D-4CBF-85BB-4D66BB204E90 at 17:57:11 Central with zero exact-path detections.
  These intermediate scans do not explain or clear the earlier quarantines and do not replace the final clean-commit build/scan.
- Shared policy is now v2, but the owning CFO reports this task still needs migration and two unrelated runs are active.
  Keep the managed gate held until that migration is explicitly confirmed.
- Real browser verification passed native CFO acceptance, two-file range delivery to the CFO queue with zero worker sends, visible unchanged context review, lost-response/SSE success with one request, stale-session refusal, preserved recipient-switch drafts, and active-to-completed selection opening Changes.
- The CFO independently verified native pointer drag, live connectors, saved positions after reload, collapse/expand, Arrange, zoom/Fit and completed-card review at 390 px.
  Its three visual findings are addressed: initial scale is at least 80%, the narrow header separates its rows, and compact lineage uses the small accessible chevron instead of a footer strip.
  Final affected-layout checks and refreshed screenshots remain before committing.
- Final affected browser checks passed at 390 x 844 and 1280 x 720; the CFO accepted the refreshed desktop/narrow screenshots and all visual findings.
  The stable example at :58749 again includes queued, active and completed records.
- Full Go tests and vet passed, followed by final frontend typecheck/lint/ten tests/build.
  Native CFO and worker receipt counts are each one; three distinct review comments remain in the isolated CFO queue.
  Attributable native CPU/RAM and twenty direct/twenty PowerShell hook samples are recorded in the revision evidence.
- Final source/evidence commit is next, followed by an external clean-commit build, exact-artifact Defender scan and manifest outside tracked source.
  Shared task migration and the managed gate remain with the supervising CFO; no retry or bypass is authorized during this hold.
- Committed the approved UI and evidence as `a45e2cd`.
  The external clean-checkout build exposed mixed HTML line endings under Windows checkout conversion; pinning Vite input/output HTML to LF restored the committed asset comparison.
  A follow-up build correction commit precedes the final executable; no generated file was hand edited.
