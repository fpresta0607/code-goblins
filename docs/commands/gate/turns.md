# cfo gate turns

`cfo gate turns [--json]`

Show the line `cfo gate test`, `cfo gate prepush` and `cfo gate turn` runs take their turns in: which run holds the turn, for how long and under what budget, what that run says it is doing (how many packages are done, which tests are running and for how long, its last line of output), which runs wait, in order, and the machine's memory when a run would wait for it.
On a line of two slots it also shows what each run is expected to use and what each holder uses now.
When memory and commit, less the about 1 GB a new goblin's session takes, are short of the floor for the line's next run, its last line says the supervisor starts and resumes no goblin by itself, keeping the test slots open.
A gate shows a step's output only once the step has ended, so this is where a run's wait and its progress are read while they last.
`--json` gives the same as one JSON object whose keys stay as they are, for the board to read: `capacity`, `is_weighed`, `holding` and `waiting`, each run with `who`, `pid`, `since`, `budget_seconds`, `now`, `estimate_bytes`, `used_bytes` and `is_low_priority`, `memory` with `available_bytes`, `commit_available_bytes`, `floor_bytes` and `is_short`, and `hold`, which says why no goblin starts or resumes by itself and is empty when one may; see [docs/pipeline.md](../../pipeline.md#taking-turns)
