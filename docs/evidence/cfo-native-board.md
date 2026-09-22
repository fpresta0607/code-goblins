# Native board implementation evidence

Verification date: 2026-09-22, Windows 11, assigned `codex/cfo-native-board` worktree.
Implementation used the confirmed GPT-6 Astra max worker without dispatching another CFO.
The architecture remains a Go supervisor with an embedded React/TypeScript/Vite browser UI; Tauri packaging is outside this change.
The managed no-mistakes result and final artifact manifest are reported through task status and `cfo notify cfo-native-board` after implementation verification.
No merge is authorized by this report.

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

The operator's codex-then-claude primary chain and frozen v1 task policy remain untouched.
A managed gate refusal must be reported exactly and must not be bypassed with a native no-mistakes mutation, policy migration, configuration overwrite, or manual push/PR.
