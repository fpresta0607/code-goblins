# goblins shared

`goblins shared [--json]`

Print the state of each service the whole fleet shares, as the supervisor last checked it: the gate daemon, the machine's line for heavy runs, the Docker engine and the Scrawl server.
Any goblin may run it under any harness, and it prints the same fields under each.
It only reads the one record the supervisor keeps, `state/shared-services.json`, and asks no service anything, so a read adds no load to a busy one.
Nothing in it starts, stops or restarts a shared service.

The supervisor checks every minute (issue 2365):

- The gate daemon by its `daemon.pid`, that process and the runs its database holds open, then by one `no-mistakes daemon status`. A live daemon that missed that short health reply is `busy`, never `stopped`, with how many runs it serves; `ready` answered, `stopped` has no process and `unknown` could not be read.
- The heavy-run line: who holds its turns, never a light run beside them, who waits in it, and the memory and commit free against the floor its next run needs. It is `free`, `full` when every turn is held, or `short` when memory is under the floor, which is why a waiting run is not started.
- The Docker engine: `running` when it answers, `starting` for a start `goblins services up` made within five minutes, else `stopped`, with the tasks whose stacks hold it.
- The Scrawl server (issue 2380): its pid and the share of one processor core it used since the last check, or since it started when the last check read no server or another one. It is `running`, `spinning` at half a core or more over a reading of at least 30 seconds, `stopped` when none runs, since the next review starts it, or `unknown`. A spinning server still serves reviews, so a goblin goes on using it and never ends or restarts it; it raises one `shared` wake to the CEO, and one more for a later spin once a check finds it quiet or stopped.

Each service carries when it was checked, what a goblin does now (`go`, `wait` or `ask`, with how) and, when waiting, the check that can change the answer.
It also names the goblins whose latest report is a wait on it: report one with `goblins notify <task> --waiting-on gate-daemon|memory|docker "<why>"`, the heavy line's wait being a wait on memory, and do not poll.
However many goblins wait on the gate daemon or the Docker engine while it is down, the supervisor raises one `shared` wake to the CEO, and one more once it is usable again, naming the goblins to tell, each told once.
It takes a service for down or usable again only once two checks in a row agree, so a daemon that misses its health reply now and then raises no wake at each miss.
An outage that was waiting to come back by itself and then reads stopped, a busy daemon whose process died or an engine whose start ran past five minutes, raises one more `shared` wake: starting it is now the CEO's to decide, never a goblin's.
A wait on either ends by itself, so the stall check does not wake the CEO for each goblin that waits on one.
The CEO's digest shows the same states on its `SHARED:` line with the count waiting on each.

A record older than three checks is marked stale (`is_stale` in `--json`, beside `age_seconds`), and no record yet exits 1: either way, ask the CEO whether the supervisor runs.
