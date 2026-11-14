# goblins gate turns

`goblins gate turns [--json]`

Show the line `goblins gate test`, `goblins gate prepare`, `goblins gate prepush` and `goblins gate turn` runs take their turns in: which run holds the turn, for how long and under what budget, what that run says it is doing (how many packages are done, which tests are running and for how long, its last line of output), which runs wait, in order, and the machine's memory when a run would wait for it.
On a line of two slots it also shows what each run is expected to use and what each holder uses now.
Each light run, which goes beside the slots, is shown apart from the slots' holders, with its bound and the memory it uses of what it is expected to use.
When memory and commit, less the about 1 GB a new goblin's session takes, are short of the floor for the line's next run, its last line says the supervisor starts and resumes no goblin by itself, keeping the test slots open.
A gate shows a step's output only once the step has ended, so this is where a run's wait and its progress are read while they last.
Each waiting run after the first says why it stands behind the one before it, as `behind run 1: a gate's Test step goes ahead of a goblin's own turn`.
Every change of the line takes its lock, `custody-mutation` in the line's store, for milliseconds; while one live process has held it a minute or more, no run joins the line, moves in it or takes a turn, so a free turn reads `turn: free, but no run can take it` and a `lock:` line names the owner's pid and how long it has held the lock.
`--json` gives the same, less the reason a waiting run stands behind the one before it, as one JSON object whose keys stay as they are, for the board to read: `capacity`, `is_weighed`, `holding`, `light` and `waiting`, each run with `who`, `pid`, `since`, `budget_seconds`, `now`, `estimate_bytes`, `used_bytes` and `is_low_priority`, `memory` with `available_bytes`, `commit_available_bytes`, `floor_bytes` and `is_short`, `hold`, which says why no goblin starts or resumes by itself and is empty when one may, and `stuck_lock`, the lock's `pid` and `since` while it is held a minute or more and null otherwise; see [docs/pipeline.md](../../pipeline.md#taking-turns)
