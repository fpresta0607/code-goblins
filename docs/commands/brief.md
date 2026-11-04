# cfo brief

`cfo brief <id> --project <p> [--kind <kind>] [--mode <m>] [--surface <s>] [--risk <r>] [--reason "<assessment>"] [--delivery-exception "<authority and reason>"]`

`cfo brief ready <id>`

Scaffold a task brief at `data/<id>/brief.md`.
`--surface internal|product|mixed|unknown`, `--risk low|<sensitive-surface>` and `--reason "<assessment>"` describe the concrete change for the project's delivery posture.
Omitted or uncertain inputs select no-mistakes; `--mode` can request stricter validation, and a narrower exception needs explicit Overlord authority recorded with `--delivery-exception` at brief and spawn.
A narrower `--mode` with neither a qualifying assessment nor that exception writes no-mistakes and prints why on stderr.
The Delivery section records this assessment for the spawn's resolver; see [Delivery intake](../contract/delivery.md#delivery).
The brief records the resolved checkout, so a bare name and its path write the same brief.
Scaffolds created by `cfo brief <id>` start with `ready: false` in Delivery.
Replace every TASK, ACCEPTANCE and CONSTRAINTS placeholder, finish the credentials and launch settings, then run `cfo brief ready <id>`.
Keep it false while editing: neither the supervisor nor explicit spawn starts an unfinished brief.
Admission requires nonempty Task, Acceptance criteria and Constraints sections without generated placeholders, explicit Authentication `credentials:` and `mcp:` choices, exactly one valid Delivery `mode:` and one `ready: true` line.
Empty or missing requirements and unfinished or malformed choices refuse dispatch before routing or policy freezing.
An optional `connectors: none` line beside them leaves out the [crawler connector](../crawler.md) every goblin otherwise carries; any value but `none` or `crawler`, or a second line, refuses the brief.
A helper's spawn checks the [helper contract](../contract/dispatch.md#helper-goblins) instead, so a helper's brief is never run through `cfo brief ready`.
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
