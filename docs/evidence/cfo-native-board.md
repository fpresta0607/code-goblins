# Native board implementation evidence

Verification date: 2026-09-22, Windows 11, assigned `codex/cfo-native-board` worktree.
Implementation used the confirmed GPT-6 Astra max worker without dispatching another CFO.
The architecture remains a Go supervisor with an embedded React/TypeScript/Vite browser UI; Tauri packaging is outside this change.
The managed no-mistakes result and final artifact manifest are reported through task status and `cfo notify cfo-native-board` after implementation verification.
No merge is authorized by this report.
The first sections preserve the original implementation checkpoint; the final section records the approved two-view revision and current delivery hold.

## Automated checks

- `GOMAXPROCS=2 go test -p 1 ./...` passed across the repository.
- `go vet -p 1 ./...` passed.
- The changed supervisor, nativehook, harness, pipeline, spawn, watch, and cmd/cfo packages passed a subsequent uncached run; cmd/cfo took 72.956 seconds.
- Final persistence admission/size regressions passed, followed by ten restart regression runs (32.298 seconds) and the full supervisor package (39.142 seconds).
- `npm ci`, typecheck, ESLint, five frontend behavior tests, and the production Vite build passed.
- A clean lockfile install/build regenerated all eight embedded output files byte-identically.
- npm reported zero dependency advisories; `go mod verify` verified the unchanged module inputs.

Every test invoking `cfo` or `go test` cleared both `CFO_HOME` and `CFO_STATE_OVERRIDE` or set both to the isolated fixture.
The same requirement belongs in the managed gate intent.
The first lockfile reinstall was blocked by the task-owned Vite process holding its native Rolldown module on Windows; stopping that process and reinstalling resolved the lock.
The temporary comparison script initially used PowerShell property enumeration incorrectly; an explicit Python SHA256 manifest comparison confirmed identical output.
Neither issue was ignored as a passing check.

Regression coverage includes actual Windows PowerShell 5.1/npm-style shim argument handling, Codex max effort, one verified brief delivery, failed atomic writes, retryable metadata failures, a 301-record backlog across the 256-record batch, a 4,097-record admission overshoot, 512-session retirement, bounded task history, missing-hook recovery, lineage cycles/reparenting/replay, interrupted external actions, exact-HEAD gate evidence, returned custody, late gate/PR transitions, landed main content, retained task diff bases, and strict local API/path boundaries.

## Native and browser acceptance

The isolated fixture home is `%TEMP%\cfo-board-windows-33eb9388f1e6449b8070a73a12dfd69f`.
Its named Herdr session is `cfo-board-test-d06265d5bf4b`; the board URL during review is `http://127.0.0.1:53062`.
Task-owned Chrome tab 7 was used while unrelated tabs were preserved.
The fixture uses local git commits, installed native hook helpers, real Herdr prompt/foreground-process APIs, and a deterministic test executable with no model calls.
Synthetic lineage payloads exercise supported native event contracts; the fixture nodes do not claim to be live model agents.

| Check | Observed result |
| --- | --- |
| Hook setup | Claude, Codex and Pi installation repeated against isolated configuration; unrelated configuration preserved |
| Task selection and previews | Two changed files, syntax tokens, added/deleted line numbers, unified/split/code views, contextual line selection |
| History and terminal | Two real fixture commits, commit diff syntax rendering, bounded on-demand Herdr capture |
| Lost response plus SSE success | Request `60d4b249-d169-4fb6-88bc-3bc147ff3dc6`, one POST, one harness receipt, succeeded outcome, disabled duplicate retry |
| Browser-closed crash | Terminated only fixture PID 21372, Herdr foreground returned to shell PID 33504, native active remained recorded, runtime unavailable, independent task evaluation reached review |
| Supervisor restart | Browser showed Reconnecting and retained evidence, then Live without a reload; all five session nodes and the succeeded feedback action persisted |
| Lineage | Reported CFO/goblin/child/background nodes, unknown parent explicit, collapse/expand works, task dependency separate |
| Child identity | Absent child model displayed Model unreported in node and drawer; no inherited max effort |
| Responsive UI | Actual 390 x 844 viewport, 390 px document and drawer widths, no document overflow |
| Empty/error states | Search with no matches, first-run empty fixture, disconnection alert, and known uncertain feedback outcome rendered |
| Vite development | Real empty board and SSE worked through the development proxy; mutation reached generation validation, untrusted Origin remained HTTP 403 |

The CFO reviewed desktop board, unified diff, successful/uncertain feedback, desktop/narrow lineage, and narrow details and found them coherent and readable.
Screenshots are preserved alongside this report in [cfo-native-board/](cfo-native-board/).

## Attributable Windows resources

These are observed short samples, not capacity or performance guarantees.
CPU is elapsed process CPU seconds, not machine-wide utilization.
Working sets include shared pages and must not be interpreted as unique physical memory.
Native samples exclude transient Git/PowerShell child CPU; hook launch latency is measured separately below.
The harness is the deterministic fixture and does not represent an LLM harness's token or memory cost.

| Process / sample | PID | Duration | CPU seconds | Working set MiB | Private MiB |
| --- | --- | --- | --- | --- | --- |
| Supervisor, quiet | 39704 | 30.16 s | 0.0312 | 18.89 | 52.57 |
| Herdr, same quiet sample | 35864 | 30.14 s | 2.0781 | 29.28 | 13.81 |
| Fixture harness, quiet | 39176 | 30.09 s | 0 | 7.41 | 12.78 |
| Supervisor, active | 37504 | 14.52 s | 0.3438 | 59.27 | 53.43 |
| Herdr, same active sample | 35864 | 14.57 s | 1.2344 | 29.28 | 13.81 |
| Fixture harness, active | 39176 | 14.56 s | 0.1094 | 8.71 | 13.86 |
| Isolated Chrome process group, quiet | Root 17224 | 20.27 s | 0.0312 | 488.68 | 236.09 |
| Same Chrome group, active | Root 17224 | 8.96 s | 3.2187 | 625.79 | 360.49 |

The active native sample sent three real fixture prompts and read snapshots while hooks/actions progressed.
The browser active sample switched board/lineage/details and loaded code previews four times; reported JavaScript heap was 13,047,926 bytes.
Browser measurements used a separate task-owned Chrome profile with its eight attributable browser/GPU/utility/renderer processes, excluding unrelated open browser sessions and the AXI bridge.
The measurement browser and Vite dev server were stopped after the checks.
Per-PID raw measurements are preserved with the screenshots.

| Hook invocation path | Samples | Median | p95 | Maximum |
| --- | --- | --- | --- | --- |
| Direct native executable | 20 | 16.31 ms | 24.24 ms | 24.49 ms |
| Installed PowerShell helper | 20 | 279.25 ms | 503.77 ms | 597.22 ms |

These measurements include process startup and synchronous spooling in an isolated home.
The helper's PowerShell startup dominates this measured hook path, so an activity-heavy harness can incur appreciable overhead despite a small idle supervisor.
The implementation does not claim that cost is negligible.

## Security and artifact provenance

Defender reported ThreatID `2147731250`, `Trojan:Win32/Bearfoos.A!ml`, and successfully quarantined two earlier artifact paths around 10:03 and 10:08 Central:

- `%TEMP%\cfo-board-windows-33eb9388f1e6449b8070a73a12dfd69f\cfo.exe`, including fixture supervisor PID 34556.
- `%TEMP%\cfo-native-board-test.exe`.

Execution of those flagged artifacts stopped.
No exclusions were added, protection was not disabled, and quarantined files were not restored.
The earlier detections remain unexplained; neither rebuilds nor later clean scans establish that they were false positives.
Detection records and matching scan start/finish events are retained alongside this report.

The tracked/untracked source changes were inspected against the feature scope, including command entry points, subprocess paths, hook helpers, frontend configuration, and the explicit fixture harness.
Go dependency files are unchanged, module verification passes, and the executable build information names the expected module, Go 1.26.5, Windows amd64, CGO disabled, and the pinned yaml.v3 checksum.
The frontend lockfile uses pinned React, React DOM, Prism, TypeScript, Vite, and lint dependencies; no runtime service or arbitrary command endpoint was introduced.
These provenance checks and a zero-advisory npm audit do not resolve a malware classification.

Intermediate artifact `%TEMP%\cfo-native-board-v2.exe` had SHA256 `2F81EBB4B271BFB2DA593E1903C5FF8220943BA47631F4D5E92BE98C14C49510`.
Its targeted Defender scan `{C501B5F1-FF90-49EC-80DB-3E422143797D}` completed at 10:15:54 Central with no detection recorded for that artifact, independently confirmed by the CFO.
The later review artifact `%TEMP%\cfo-native-board-review.exe`, SHA256 `640168DFBF52C6D9B8CADEE14B54B195D93ED73A94F8BC36630A682466168780`, completed targeted scan `{BFBE6CFA-D68C-4D6E-9677-EDC1A8C22854}` with no detection recorded for that artifact before its browser checks.
Precommit smoke artifact: `C:\Users\fpres\AppData\Local\Temp\cfo-native-board-final.exe`, 13,577,728 bytes, SHA256 `E6F1230945414BFB14F60009224058C236FCE2ED7351BEE223B6068E4585B90D`.
Targeted Defender scan `{99DE9B93-784D-4AF3-8C3E-7B4CD76DDF82}` started at 10:50:32.588 and finished at 10:50:32.742 Central with zero detection records for that path.
Real-time protection remained enabled; engine `1.1.26080.3`, signatures `1.459.332.0`.
The [precommit smoke manifest](cfo-native-board/precommit-artifact.json) records that build's information, protection versions, hash, and matched scan identity.
The artifact was built before this implementation commit, so its embedded VCS metadata correctly identifies baseline `27ce7eecb87718442d8285f682b688f7914fbe5a` with `vcs.modified=true`; the report does not misrepresent it as a build from a clean committed tree.
The final test rerun exposed an assertion racing startup reconciliation's legitimate extra action; the test was corrected to verify the original durable completed action instead of an exact action count, then rerun repeatedly.

The final delivery executable is rebuilt outside this worktree after committing these verified sources and evidence.
Its manifest belongs in `C:\dev\code-goblins\data\cfo-native-board\final-artifact.json`, outside tracked source, and records the actual delivery commit with `vcs.modified=false`, final SHA256, and a fresh targeted Defender scan.
Recording that manifest must not alter the clean delivery commit.

At that original checkpoint, the operator's codex-then-claude primary chain and frozen v1 task policy remained untouched, and the managed gate refused shared config drift.
That refusal was reported without a native mutation, configuration overwrite, or manual push/PR.

## Approved two-view revision

The later user-approved generated design supersedes the intermediate single-canvas layout.
The implementation now renders mutually exclusive Board and Orchestration views with one persistent contextual pane, semantic goblin artwork, actual native CFO/worker capture and verified submitted messages.
The three task columns use raised rounded surfaces and 120-121 px desktop cards with 80 px avatars.
Initial orchestration scale is at least 80%; explicit Fit can go smaller without changing parentage.
All three final visual findings were resolved and independently accepted by the supervising CFO.

The retained example is `http://127.0.0.1:58749`, backed by temporary home `cfo-board-windows-3c143d71c82d408584aaccd5249610aa` and isolated Herdr session `cfo-board-test-8891404635a4`.
It has queued, active and completed task records plus five-level native lineage and an explicitly unlinked root.
Completed example evaluation is seeded acceptance data after comparing its local main contents, not a production managed-gate result.
Its actual native CFO/worker processes are deterministic fixtures with no model calls; the UI labels Example workspace and does not invent model responses.

| Revision check | Result |
| --- | --- |
| Automated checks | Full `go test -p 1 ./...` and `go vet -p 1 ./...` passed with isolated test variables and GOMAXPROCS=2; cmd/cfo took 93.092 s |
| Frontend | Final typecheck, ESLint, ten behavior tests and Vite production build passed; no new dependency |
| CFO identity | Required registered-agent delivery, stale/missing registration, changed process, registration write fence, interrupted delivery and full-history rejection tests passed |
| Capture bounds | One shared CFO/worker capture slot and eight-second timeout regression passed; visible-pane polling is sequential |
| Native conversation | One CFO submission produced one CFO receipt; a later intentional worker instruction produced one worker receipt and real captured output |
| Review separation | Two files, new-side lines 2-3, produced two CFO review records with exact HEAD/fingerprint and zero worker receipts at that checkpoint |
| Context review | A third comment on visible unchanged old-side line 1 produced its own CFO record |
| Ambiguous response | Lost response plus SSE success retained one request ID, one POST, one queue record and the known outcome across recipient changes |
| Session replacement | An old draft stayed bound to its original task session and disabled sending after the snapshot changed |
| Completion selection | Active selection, streamed verified completion, then clicking that Completed card opened Changes, collapsed Terminal and retained the draft |
| Pointer and keyboard | CFO independently verified drag/connectors/save/reload/collapse/Arrange/zoom/Fit; worker verified Alt+ArrowRight movement and visible storage failure |
| Truthful activity | Accepted worker feedback displayed one pulse, which expired; stale/disconnected and wrong-session pulses are excluded by behavior tests |
| Narrow layout | At actual 390 x 844, page width was 390, header did not overlap, compact chevrons replaced the footer strip, and deep nodes named actual parents |
| Child details | Missing native child model stayed Model unreported; no parent terminal appeared, and the empty state linked the owning task |

Browser screenshots and structured outcomes are retained in [cfo-native-board/](cfo-native-board/), including `revision-board-desktop.png`, `revision-orchestration-desktop.png`, `revision-orchestration-narrow.png`, `revision-child-narrow.png`, `revision-deep-lineage-narrow.png`, `revision-worker-desktop.png`, `revision-review-delivery.json` and `root-pointer-review.json`.
The worker used its named task-owned Chrome session; the CFO used and then closed a separate review tab.
Unrelated pages and production workers were preserved.

### Revision resource observations

During a 30-second sample with the CFO pane visible and sequential native capture enabled, supervisor PID 6108 used 0.4063 CPU seconds, 65.78 MiB working set and 53.47 MiB private memory.
The same sample measured Herdr PID 46956 at 1.3906 CPU seconds / 33.57 MiB working set, fixture worker PID 26796 at 0 CPU seconds / 8.27 MiB, and fixture CFO PID 4080 at 0 CPU seconds / 10.66 MiB.
These are attributable short process samples, excluding transient child CPU and unrelated browsers; they are not unique physical-memory or capacity estimates.
The earlier separately attributed browser measurements remain historical observations rather than measurements of this revision.

Twenty accepted direct hook invocations measured median 41.66 ms, p95 74.46 ms and maximum 77.49 ms.
Twenty installed PowerShell helper invocations measured median 387.07 ms, p95 768.25 ms and maximum 1937.41 ms.
The initial measurement payload omitted required role/cwd context and was refused; only the corrected accepted invocations enter these figures.
PowerShell launch overhead remains material and is not hidden in the supervisor's idle CPU number.
Raw samples are in `revision-resources.json` and `revision-hook-latency.json` beside the screenshots.

### Revision security and delivery boundary

The earlier Bearfoos quarantines above remain unexplained and are not cleared by subsequent clean scans.
The v5 review executable had SHA256 `3CEF1ADCB24BAE475DAC68BA6B43FD02C08553A060A45459E2029D2ED7220E29`; exact-path scan `{C70E6E84-7879-4E6E-BC38-40D2B6B02485}` completed at 18:20:56 Central with zero recorded detections for that path before execution.
The final revision executable is built outside the worktree only after this source/evidence commit, with its exact SHA256, delivery commit, `vcs.modified=false`, build information and matched Defender scan recorded in `C:\dev\code-goblins\data\cfo-native-board\revision-final-artifact.json`.
The original `9c9af11` executable and manifest remain retained.

The reported credential incident concerns `GITHUB_TOKEN` from the inherited process environment, with upstream provenance undetermined.
The literal scan found zero in the scanned worktree files, branch commit objects, named artifacts and known task output locations, and five matches in the existing worker transcript.
It stopped at that disclosure; remaining logs were not scanned, so neither universal absence nor a no-transmission claim is supported.
The credential value is not reproduced here, and the operator's ruling leaves credentials and transcripts unchanged.

The supervising CFO reports global v2 policy applied but this task still on its frozen v1 snapshot, awaiting an idle migration window while unrelated runs continue.
The worker has not retried the gate or changed shared settings; the next managed gate remains required and externally held, with no push, PR or merge claimed.

The first external clean-checkout rebuild caught a Windows reproducibility defect: Vite retained CRLF template newlines while inserting LF asset tags, producing mixed-line-ending HTML.
Pinning the source template and embedded HTML/JavaScript/CSS to LF in `.gitattributes` fixes both the actual lockfile-build comparison and Windows Git's modified-file detection after regeneration.
The final artifact is built from the subsequent clean commit containing that correction, with no hand edits to generated assets.

## Native terminal and focused review candidate, September 23 UTC

This subsequent revision separates Board review from Orchestration's actual native terminal.
Board retains inline, revision-bound file/range annotations to the CFO and contains no terminal or standalone chat composer.
Orchestration uses the installed Herdr protocol22 native screen stream, observes by default, and claims input only after Connect input.
The xterm.js renderer and fit addon are pinned, loaded on demand and distributed with their MIT license notices.
Workspace details exposes scoped names/configuration and reported model evidence without environment values, MCP commands or headers.
The verified primary CFO can deliberately publish a modal with `cfo question`; worker notifications and natural-language questions alone cannot open it.

| Candidate check | Evidence and boundary |
| --- | --- |
| Frontend | TypeScript, ESLint, eleven behavior tests and Vite production build passed |
| Backend | Full supervisor package passed in 47.574 s; Herdr package passed in 1.559 s; production home/state overrides cleared, GOMAXPROCS=2 |
| Terminal regressions | Blocked-write cancellation/teardown, changed/exited identities, stable binding across PR metadata, frame gap/full rebaseline, ownership refusal and non-replayed input passed |
| Questions | CFO-owned publication, durable ID conflict, corrupt/overflow isolation, stale CFO supersession, answer idempotency, interrupted-delivery uncertainty and storage rollback passed |
| Retention reproduction | Pre-fix source overlay loses unseen deferred questions for distinct and equal timestamps; the fixed test preserves each record through capacity rollover and restart at the 128-record bound |
| Metadata | Scoped MCP/environment name redaction and reported-versus-configured model behavior passed |
| Additional source checks | Scoped `go vet -p 1` for supervisor/Herdr/cmd-cfo passed; `git diff --check` passed; production npm audit reported zero vulnerabilities |
| V2 browser only | Actual isolated fixture typing/Enter, complete multiline paste, Escape remaining in the terminal, PageUp not scrolling the page, Shift+Escape releasing input and keyboard focus, and oversized Unicode paste rejection without disconnection |
| V2 latency sample | Seventeen input requests: median 80.2 ms, maximum 169.8 ms, zero HTTP failures; short sample only |
| Latest layout/CSP | Root's clipping and compressed-column defects are corrected in source; final browser acceptance remains pending |
| Remaining acceptance | Final responsive screenshots, sustained typing, copy/native scroll/interrupt checks, final modal and inline review round trips, and coordinated real CFO/Codex tests are not yet complete |

The retained v2 candidate is `http://127.0.0.1:63790`, SHA256 `00E20173BACFCDF372A43F25C18E2572A53852E4791C31B74874468241E26DFB`.
Its screenshot is `C:\dev\code-goblins\data\cfo-native-board\terminal-v2-worker-readonly.png`; the root's independent UI review is `root-terminal-ui-review.json` in that directory.
The processes used for these input checks are deterministic acceptance fixtures, not live model evidence.
Three additional harmless fixture receipts confirm the typed nonce and two pasted lines; no real CFO input or control was used.
The original stable preview at :58749 remains running.

Automatic approval review rejected restarting the v2 preview and then launching a fresh isolated preview for the scanned v3 binary.
Both were refused before execution with the sole stated reason `blocked by policy`.
Exact commands and action boundaries are retained in `C:\dev\code-goblins\data\cfo-native-board\preview-approval-block.md`.
The owning CFO instructed the worker to stop equivalent launch attempts, finish source/unit checks and provide a clean committed artifact plus exact scan before coordinating approval.
The latest CSS/CSP fixes cannot be certified from the still-running v2 preview.

Native input is bound to the exact terminal, task generation, routing and process identity, with custody checked separately before each write.
Herdr has no input acceptance acknowledgment and cannot atomically bind a write to the expected foreground PID; an exit between proof and write can expose the same PowerShell terminal to those bytes.
The bridge rejects known exited/changed identities, never automatically takes over, and never retries uncertain input.
It preserves the real ANSI screen while omitting unsupported Kitty/graphics negotiation; input and frame contents are not persisted by the bridge.

The previous unexplained Bearfoos quarantines and the precise credential-scan limits above remain in force.
The real CFO's production registration is known stale; its owner must establish a verified registration only in the agreed isolated test home before the authorized live round trip.
The final candidate is built from the new clean commit outside this worktree, with its commit, `vcs.modified=false`, SHA256 and completed exact-path Defender scan recorded externally in `terminal-final-artifact.json`.
The owning CFO reports task/global v2 policy migrated with high-risk three-cycle review preserved; the managed gate remains required after the new acceptance work, with no gate, push, PR or merge claimed at this checkpoint.

## Command Center and verified review candidate, September 23 UTC

The approved Command Center modal now presents real recommended choices plus Other, and persists the actual submitted answer across tabs and ambiguous responses.
Primary session guidance and command help route explicit decisions through `cfo question` across supported harnesses without pretending a normal message answers a pending native prompt tool.
The approved full-height workspace panel uses larger text, native editor actions, quiet connection details and inline diffs; the generated canvas illustration is anchored at the bottom right.
Missing or shared native transports show concise empty states, and an unlinked session does not borrow the primary CFO's workspace.

New review annotations pin the primary recipient separately from task generation, revalidate exact diff context and use only `CFOConnection.Send`.
The previous wake-only transport did not establish native receipt, so its old queue records remain intact while new reviews create no second actionable wake.
Unit integration tests cover two distinct files, exact server-derived ranges, required native adapters for Claude/Codex/Pi, zero worker sends, duplicate/restart suppression, stale task/primary identity, legacy unpinned actions and post-acceptance crash uncertainty.
These tests use controlled Herdr adapters; actual native-process and model/browser receipts for this candidate remain acceptance work.

Explicit bounded `cfo present` receipts support task and verified primary presentations without opening a page or gating work.
Primary presentation targets stay `primary-cfo` when native session discovery arrives later, so the same report can still refresh and end.
The late-discovery regression failed with `activity ID already used` before this correction and passed afterward.
Pending same-ID records preserve identity, chronology and terminal end state before ingestion.
Accepted send and creation effects require exact native parent evidence for a connector; otherwise only the destination is highlighted.
Independent deadlines prevent unrelated traffic from extending an old effect, and hidden-tab, reconnect and instance boundaries suppress replay.
Screen reader output is an explicit saved xterm option; default Unicode InsertText and bounded adjacent-text coalescing are implemented, with paste/control/resize/scroll barriers preserved.

| Check | Result |
| --- | --- |
| Frontend | TypeScript, ESLint, 19 behavior tests and production Vite build passed |
| Broad Go sweep | Every package except `cmd/cfo` passed; supervisor completed in 90.253 s |
| Command regression | The sweep found an obsolete exact nudge string in three resumed-session cases; after updating that assertion, the full `cmd/cfo` package passed in 141.474 s |
| Go vet | `go vet -p 2 ./...` passed |
| Final primary-target correction | Focused primary presentation, send receipt and activity tests passed in 2.696 s; supervisor vet passed |
| Editor behavior | Genuine linked worktree, literal spaces/non-ASCII arguments, primary/nested/missing-path rejection, local origin/token and Electron flag sanitation passed without launching an editor |
| External evidence | `command-center-go-test.log`, `command-center-cfo-recheck.log`, `command-center-go-vet.log` and `command-center-milestones.json` under the fleet task directory |

Go checks ran with production home/state overrides and `GITHUB_TOKEN` removed from the child process, and `GOMAXPROCS=2`.
The original full-run failure log is retained beside the successful recheck rather than overwritten.
No credential value was inspected or printed for this revision.
The existing disclosure, unscanned-log limits and unexplained historical Bearfoos detections remain as documented above.

The final candidate is built outside the worktree from this clean source commit, with its exact executable/hash/build metadata and completed targeted Defender scan recorded in `C:\dev\code-goblins\data\cfo-native-board\command-center-final-artifact.json`.
The parent-owned `http://127.0.0.1:62197/` preview still serves the earlier `b242192` build and does not certify these changes.
Fresh responsive screenshots, actual Unicode typing/coalescing latency, independent pulse lifetime/hidden-tab behavior, question/Other round trips, and two-file native CFO review receipt remain pending on the new candidate.
No real primary CFO input, shared configuration mutation, gate, push, PR or merge occurred in this revision.

## Native terminal layout and truecolor follow-up

Root acceptance on the scanned `3309a5b` fixture verified exact Unicode typing/paste, two-file native CFO annotations with zero worker annotation receipts, Other answers and durable cross-tab answers.
Those are deterministic native-process results, not actual-model receipts; the root's record is `root-native-acceptance-3309.json` in the fleet task directory.
The same pass reproduced a clipped terminal at 1280 by 720, and a later read-only view of the actual Codex screen exposed blocked ANSI truecolor attributes.

Orchestration now starts with Workspace details collapsed, preserves text sizes, and scrolls its own pane when details or terminal options need more space.
Board retains the approved expanded workspace and review layout.
The terminal's minimum viewport is 160 px, leaving its caption and options visible in shorter windows without reducing its font.
Browser geometry checks used the changed source CSS and identical disclosure markup in a dedicated tab of the scanned `3309a5b` preview; they are not a compiled replacement acceptance claim.
The controls were inside the pane at 1280 by 720, 1280 by 600, 390 by 844 and 390 by 600, with no page horizontal overflow.
At 390 by 600, expanding both disclosures and scrolling the pane also kept all terminal options reachable.
Measurements are retained as `worker-terminal-layout-*.txt`; the browser screenshot command timed out, so no new worker screenshot is claimed.

The HTTP policy now permits `style-src-attr 'unsafe-inline'` for xterm 6's DOM-rendered colors.
This permits CSS attributes across the page, while style elements retain their nonce or same-origin restriction and scripts, connections, framing and base URLs keep their prior protections.
No renderer interception, global DOM patch or dependency was added.
The directive-semantics regression failed before the change and passed after it; origin/idempotency and late-primary-discovery regressions passed in the same focused run, followed by supervisor vet.
Frontend type checking, ESLint, all 19 behavior tests and the production build passed.

The replacement executable is built once from the clean follow-up commit outside the worktree and scanned before execution, with provenance in `command-center-final-artifact.json`.
The owning CFO still needs to confirm the compiled layout and actual Codex computed colors on that executable.
The authorized actual-Codex question test uses only this worker's existing process and a separate temporary primary registration, then waits for the owning CFO's modal answer.
Its result is recorded outside tracked source, and the managed gate remains parent-owned.
