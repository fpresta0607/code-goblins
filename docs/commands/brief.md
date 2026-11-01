# cfo brief

`cfo brief <id> --project <p> [--kind <kind>] [--mode <m>]`

`cfo brief ready <id>`

Scaffold a task brief at `data/<id>/brief.md`.
The brief records the resolved checkout, so a bare name and its path write the same brief.
Generated briefs start with `ready: false` in Delivery.
Replace every TASK, ACCEPTANCE and CONSTRAINTS placeholder, finish the credentials and launch settings, then run `cfo brief ready <id>`.
Keep it false while editing: neither the supervisor nor explicit spawn starts an unfinished brief.
Its Authentication section holds a `credentials: none` line and lists the services the project's manifest declares: name the ones the task needs on that line before you spawn it.
Beside it is an `mcp: none` line, with the MCP servers whose entry in the project's `.mcp.json` holds a value listed by name to name from

For an existing completed queued brief without a ready marker, review its requirements, Delivery mode and explicit Authentication choices, then run `cfo brief ready <id>`.
This is an explicit lifecycle transition: it validates the full brief before adding `ready: true` or replacing `ready: false`.
It preserves all other authored bytes, including mode, credentials and policy choices, and leaves queue holds and existing task metadata and policies untouched.
Already-ready valid briefs are unchanged.
Incomplete or malformed briefs, missing explicit choices, and tasks with execution state or archived history refuse finalization without changing the brief.
There is no automatic conversion of existing briefs and no missing-marker admission fallback.
