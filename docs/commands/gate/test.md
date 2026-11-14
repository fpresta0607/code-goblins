# goblins gate test

`goblins gate test [--level changed\|fast\|affected\|full \| --focused] [--plan]`

This repository's no-mistakes test step, run from `.no-mistakes.yaml`'s `commands.test` as the step's baseline, runs `go vet` and `go test -json -count=1 -p 2 -timeout 45m` at the selected verification level.
It runs them without `GOBLINS_HOME` and `GOBLINS_STATE_OVERRIDE`, which a gate step inherits from the goblin's pane, without `NO_MISTAKES_GATE`, under which every `goblins hook` does nothing and the hook tests would test nothing, and without `GOBLINS_TEST_EVIDENCE`, which picks the step's own default level and a test of that default would read.
CI still runs every package before a change reaches the default branch, in its merge train's run; the local step is bounded because an agent-chosen step ran the whole suite on this machine, where supervisor alone takes about 22 minutes, and hit the 30-minute test agent limit.
With neither `--level` nor `--focused` the step, the gate's included, runs `fast`; [Verification levels](../../pipeline.md#verification-levels) owns package selection, required guards and platform deferrals.
Running the required level here as well ran the slowest packages twice, here in a median of 33 minutes and again in CI.
`--level affected` runs affected checks here, such as to reproduce what CI found; `--level full` tests every package; `--plan` prints the plan and runs nothing.
`--level changed` vets as the change requires and runs only the tests the change added or edited, each by name, and prints what it left to CI.
With no `--level` it is the level of a project whose hosted CI is its test evidence, which `GOBLINS_TEST_EVIDENCE=hosted-ci` from `goblins gate turn` says; see [A project whose hosted CI is its test evidence](../../pipeline.md#a-project-whose-hosted-ci-is-its-test-evidence).
`--focused`, the repository's `commands.test_focused`, reads `NO_MISTAKES_TEST_FOCUS` as an absolute path to a UTF-8 file, never as shell code.
Its `test <package> --- FAIL: TestName/subtest (0.01s)` and `path <relative/path>` records select each failed top-level test and every check the repository-relative repair paths reach under the same trusted affected-level planner, including transitive dependents and required guards.
A named failure already covered by a selected whole package is not run twice.
Every recognized package/test pair must finish without skipping across the run, including a run that could not narrow; removing or renaming its package or test does not discharge that requirement.
A repair it cannot narrow so, for missing failure or repair-path records, unrecognized or unmappable data, an unclassified repair path or a repair that requires the full level, runs the step's configured level over the repaired paths, or over the branch's change when the repair names no path, never the full level, which once held the machine's heavy line for hours.
Its one line says why and what it leaves to CI, which runs every package: the level the repair requires, and each failed test it could not map, by name; the failed tests it can map still run by name.
Unsafe or escaped paths and invalid UTF-8 fail explicitly.
A narrowed repair report uses `focused` scope; one that could not narrow uses the configured level.
`--focused` cannot be combined with `--level`.

`goblins gate lint [--plan]` uses the same trusted planner's vet selection at the required level, with full-tree vet for unknown changes, module changes or an unreadable policy.
It runs no tests and prints its vet plan and verdict without writing test reports or logs.

`goblins gate test` prints what `go test` prints without `-v`, and a run in which a package did not pass ends its tests' output by naming each such package and the tests in it that failed or never finished.
See [Verification levels](../../pipeline.md#verification-levels) for test reports, logs, recorded source details, admission, budgets and process cleanup.
