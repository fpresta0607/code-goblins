# cfo brief

`cfo brief <id> --project <p> [--kind <kind>] [--mode <m>]`

Scaffold a task brief at `data/<id>/brief.md`.
The brief records the resolved checkout, so a bare name and its path write the same brief.
Generated briefs start with `ready: false` in Delivery.
Replace every TASK, ACCEPTANCE and CONSTRAINTS placeholder, finish the credentials and launch settings, then set `ready: true` as the final edit.
Keep it false while editing: neither the supervisor nor explicit spawn starts an unfinished brief.
Its Authentication section holds a `credentials: none` line and lists the services the project's manifest declares: name the ones the task needs on that line before you spawn it.
Beside it is an `mcp: none` line, with the MCP servers whose entry in the project's `.mcp.json` holds a value listed by name to name from
