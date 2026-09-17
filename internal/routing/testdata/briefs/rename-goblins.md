# Brief rename-goblins

Delivery contract: mode=no-mistakes

## Project

projects/code-goblins (the cfo fleet orchestrator, Go)

## Task

code-goblins is its own open-source product now, no longer a First Mate rebuild. Rename the inherited First Mate runtime vocabulary to code-goblins branding everywhere it is user- or machine-visible:

- The Herdr workspace label `firstmate` becomes `code-goblins` (see `EnsureContainer` in internal/herdr/client.go and every test/fixture/acceptance-script reference).
- The task tab prefix `fm-` becomes `gb-` everywhere it is created, enforced, validated, parsed, or documented: internal/herdr (CreateTask label enforcement, prober identity validation in internal/monitor/prober_herdr.go), internal/fleet target resolution, internal/cleanup, cmd/cfo, tests/acceptance/plan3_windows.ps1, and all tests.
- User-facing docs and help text: README.md, AGENTS.md (command table, target description, dispatch rules that mention `fm-<id>`), cmd/cfo usage string, docs/ that describe the live contract. Historical references to upstream First Mate as the origin project may stay where they describe provenance; the product's own identity becomes code-goblins.

## Acceptance criteria

- No live code path creates, requires, or validates `firstmate` or `fm-` naming anymore; `code-goblins` / `gb-` replace them.
- `go build ./...`, `go vet ./...`, and `go test ./... -count=1` all pass.
- The acceptance self-tests pass: `go test ./cmd/cfo -run TestPlan3AcceptanceScriptSelfTests -count=1`.
- Docs (README.md, AGENTS.md, usage text) describe the new names.

## Constraints

- Pure rename: no behavior changes beyond the names themselves.
- Do NOT rebuild, replace, or touch `cfo.exe` in the repository root, and do not touch the running Herdr server, workspace, or tabs (a fleet task is live in the old workspace right now; the new `code-goblins` workspace will simply be created on first spawn after this lands - no migration, no deletion of the old workspace).
- Do not touch `.git/` or the no-mistakes gate repo config beyond what the pipeline does itself.
- One commit is fine; the pipeline handles the rest.
