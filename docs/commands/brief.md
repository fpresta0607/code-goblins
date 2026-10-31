# cfo brief

`cfo brief <id> --project <p> [--kind <kind>] [--mode <m>]`

Scaffold a task brief at `data/<id>/brief.md`.
`--surface internal|product|mixed|unknown`, `--risk low|<sensitive-surface>` and `--reason "<assessment>"` describe the concrete change for the project's delivery posture.
Omitted or uncertain inputs select no-mistakes; `--mode` can request stricter validation, and a narrower exception needs explicit Overlord authority recorded with `--delivery-exception` at brief and spawn.
The Delivery section records this assessment for the spawn's resolver; see [Delivery intake](../contract/delivery.md#delivery).
The brief records the resolved checkout, so a bare name and its path write the same brief.
Its Authentication section holds a `credentials: none` line and lists the services the project's manifest declares: name the ones the task needs on that line before you spawn it.
Beside it is an `mcp: none` line, with the MCP servers whose entry in the project's `.mcp.json` holds a value listed by name to name from
