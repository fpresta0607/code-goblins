# Supervision recovery implementation plan

**Goal:** Restore continuous supervision and usable bounded validation while preserving every worker branch, unresolved decision, browser profile, and review artifact.

**Architecture:** Keep the existing watcher, typed monitor records, native validation engine, and durable wake queue.
Give the watcher an explicit continuous process lifecycle and use one structural Herdr snapshot per observation cycle.
Use task-owned browser storage and the installed Lavish Editor instead of maintaining a second renderer.

**Tech stack:** Go, Windows process APIs, PowerShell, Herdr, native no-mistakes, Chrome AXI, Lavish Editor.

## Work and verification

1. Reproduce restored-shell classification, watcher exit after its first event, unavailable backend, parked review, and missing supervisor health through executable interfaces with temporary homes.
2. Add continuous watcher mode, bounded cancellation and probe deadlines, explicit supervisor status, and a Windows startup/recovery recipe with bounded restart settings.
3. Reconcile stopped workers and historical outcomes without using stale evidence to merge, delete, or restart work.
4. Observe native parked review and enforce repair exhaustion across CLI restarts and repeated submission.
5. Restore the authorized native trust boundary while retaining task identity, policy checks, custody, and durable budget accounting.
6. Provision unique browser session names and persistent task-owned profiles, resolve the installed MCP script using the Windows npm layout, and test real browser behavior in an isolated profile.
7. Remove Showcase, route review requests to Lavish, and retain a task recap with evidence and accurate delivery state before reporting completion.
8. Run formatting, Go tests, vet, Windows process and browser smoke checks, and the frozen high-risk pipeline.
9. Build a candidate outside the installed binary, save install/rollback instructions and the Lavish recap, and report the verified outcome to the CFO.

## Boundaries

The native engine owns fetch/config resolution; CFO owns task identity, policy checks, association and durable repair caps.
Do not install software, change shared gate configuration, restart an old gate, merge, or delete preserved work from this implementation task.
Browser feedback is optional and never blocks implementation or validation.
All tests that invoke CFO use a temporary CFO_HOME and CFO_STATE_OVERRIDE.
