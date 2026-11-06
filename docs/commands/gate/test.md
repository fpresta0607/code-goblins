# cfo gate test

`cfo gate test [--level fast\|affected\|full \| --focused] [--plan]`

This repository's no-mistakes test step, run from `.no-mistakes.yaml`'s `commands.test` as the step's baseline, runs `go vet` and `go test -json -count=1 -p 2 -timeout 45m` at the selected verification level.
It runs them without `CFO_HOME` and `CFO_STATE_OVERRIDE`, which a gate step inherits from the goblin's pane, and without `NO_MISTAKES_GATE`, under which every `cfo hook` does nothing and the hook tests would test nothing.
CI still runs every package before a change reaches the default branch, in its merge train's run; the local step is bounded because an agent-chosen step ran the whole suite on this machine, where supervisor alone takes about 22 minutes, and hit the 30-minute test agent limit.
With neither `--level` nor `--focused` the step, the gate's included, runs `fast`; [Verification levels](../../pipeline.md#verification-levels) owns package selection, required guards and platform deferrals.
Running the required level here as well ran the slowest packages twice, here in a median of 33 minutes and again in CI.
`--level affected` runs affected checks here, such as to reproduce what CI found; `--level full` tests every package; `--plan` prints the plan and runs nothing.
`--focused`, the repository's `commands.test_focused`, reads `NO_MISTAKES_TEST_FOCUS` as an absolute path to a UTF-8 file, never as shell code.
Its `test <package> --- FAIL: TestName/subtest (0.01s)` and `path <relative/path>` records select each failed top-level test and every check the repository-relative repair paths reach under the same trusted affected-level planner, including transitive dependents and required guards.
A named failure already covered by a selected whole package is not run twice.
Every recognized package/test pair must finish without skipping across the run, including a full fallback; removing or renaming its package or test does not discharge that requirement.
Missing failure or repair-path records and unrecognized or unmappable data widen the existing planner to full with an explicit reason; unsafe or escaped paths and invalid UTF-8 fail explicitly.
A narrowed repair report uses `focused` scope; a full fallback uses `full`.
`--focused` cannot be combined with `--level`.

`cfo gate lint [--plan]` uses the same trusted planner's vet selection at the required level, with full-tree vet for unknown changes, module changes or an unreadable policy.
It runs no tests and prints its vet plan and verdict without writing test reports or logs.

`cfo gate test` prints what `go test` prints without `-v`, and a run in which a package did not pass ends its tests' output by naming each such package and the tests in it that failed or never finished.
See [Verification levels](../../pipeline.md#verification-levels) for test reports, logs, recorded source details, admission, budgets and process cleanup.
