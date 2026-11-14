# goblins gate prepare

`goblins gate prepare`

This repository's no-mistakes prepare step, run from `.no-mistakes.yaml`'s `commands.prepare` before the baseline and the first Test agent invocation, installs the locked frontend dependencies a frontend-affected change needs.
It takes no arguments and no flags.
It reads the same trusted scoped change plan `goblins gate test` reads, at the fast level, and acts only when that plan holds a path under `frontend/`, a lockfile change included.
A Go-only or documentation-only change prints `goblins gate prepare: no frontend change; no dependencies installed` and starts no installer.
For a frontend-affected change it runs `npm ci` in the validation worktree's own `frontend` directory and nowhere else.
The locked install is as heavy as the tests after it, so it first takes a turn in the machine's heavy line under the memory floor, as `goblins gate test` does, and says while it waits.
An existing `node_modules` directory does not authorize reuse: every frontend-affected preparation runs the lock's own install, without sharing materialized dependencies between worktrees or adding a receipt or cache.
On Windows it invokes npm through `cmd.exe /d /c npm ci`, so standard `npm.cmd` installations work.
The installer runs without `GOBLINS_HOME`, `GOBLINS_STATE_OVERRIDE` and `NO_MISTAKES_GATE`, which a gate step inherits from the goblin's pane, as `goblins gate test` runs its checks, so a fixture's installer cannot reach the running fleet.
It reports the locked install's elapsed time once the installer ends.

Every failure is explicit and ends preparation before the baseline.
An unreadable plan refuses with exit 2 and starts no installer.
A turn that cannot be taken refuses with exit 1 and starts no installer.
Missing tooling and a nonzero installer exit refuse with exit 1 after naming the install's exit code and error.
A missing or invalid lockfile is rejected by `npm ci` itself rather than validated here.
No failure falls back to an unlocked install, to a resolved version the lock does not name, or to a later Test agent invocation that installs its own dependencies.

Activation requires the `commands.prepare: "goblins gate prepare"` entry on the default branch, from which the gate trusts it, and a reviewed installed goblins build that supports both `goblins gate prepare` and `goblins gate test`.
Its isolated regression fixtures are `cmd/goblins/gate_prepare_test.go`, with the short-temporary-root Windows case in `cmd/goblins/gate_prepare_windows_test.go`.
See [Driving a task](../../pipeline.md#driving-a-task) for the prepare step's place in the line, and [Verification levels](../../pipeline.md#verification-levels) for the plan the test step behind it runs.
