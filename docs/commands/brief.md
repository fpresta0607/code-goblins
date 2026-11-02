# cfo brief

`cfo brief <id> --project <p> [--kind <kind>] [--mode <m>]`

`cfo brief ready <id>`

Scaffold a task brief at `data/<id>/brief.md`.
The brief records the resolved checkout, so a bare name and its path write the same brief.
Scaffolds created by `cfo brief <id>` start with `ready: false` in Delivery.
Replace every TASK, ACCEPTANCE and CONSTRAINTS placeholder, finish the credentials and launch settings, then run `cfo brief ready <id>`.
Keep it false while editing: neither the supervisor nor explicit spawn starts an unfinished brief.
Admission requires nonempty Task, Acceptance criteria and Constraints sections without generated placeholders, explicit Authentication `credentials:` and `mcp:` choices, exactly one valid Delivery `mode:` and one `ready: true` line.
Empty or missing requirements and unfinished or malformed choices refuse dispatch before routing or policy freezing.
Choose credentials and MCP servers under [Credentials by need](../contract/authentication.md#credentials-by-need).
Finalizing an unheld queued brief makes it eligible for automatic start; setting `ready: true` manually as the final edit uses the same admission contract.

Complete briefs generated from backlog rows or root issues already record `ready: true` and their launch choices.
Their credential selection follows [Credentials by need](../contract/authentication.md#credentials-by-need).

For an existing completed queued brief without a ready marker, review its requirements, Delivery mode and explicit Authentication choices, then run `cfo brief ready <id>`.
This is an explicit lifecycle transition: it validates the full brief before adding `ready: true` or replacing `ready: false`.
It preserves all other authored bytes, including mode, credentials and policy choices, and leaves queue holds and existing task metadata and policies untouched.
Already-ready valid briefs are unchanged.
Incomplete or malformed briefs, missing explicit choices, and tasks with execution state or archived history refuse finalization without changing the brief.
There is no automatic conversion of existing briefs and no missing-marker admission fallback.
