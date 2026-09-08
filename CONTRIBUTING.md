# Contributing

Thanks for wanting to contribute to code-goblins.

## Workflow

1. Fork [`fpresta0607/code-goblins`](https://github.com/fpresta0607/code-goblins) and clone your fork.
2. Create a branch and make your changes.
3. Run the checks below.
4. Commit with a conventional message - the repo uses `feat(scope):`, `fix(scope):`, `docs(scope):`, and `test(scope):`.
5. Push your branch and open a pull request against `main`.

## Checks

```sh
go vet ./...
go test ./...
go build ./cmd/cfo
go build ./cmd/showcase-axi
```

CI runs the same steps on `windows-latest` for every push to `main` and every pull request.
A pull request must keep all of them green.

## Repo layout

See [Repo layout](README.md#repo-layout) in the README.

## Tests

Unit tests are deterministic: they inject fake subprocess runners and scripted clocks instead of requiring installed tools.
The telemetry and pipeline database regressions are the exception - they build real SQLite fixtures through the `sqlite3` CLI and skip themselves when it is not on PATH, so install it locally to run them (CI installs it before the suite).

```sh
go test ./...
```

The real-session acceptance suite needs real Herdr, Claude Code, Codex, and Pi, and is opt-in:

```powershell
$env:CFO_PLAN3_REAL = '1'
powershell -NoProfile -ExecutionPolicy Bypass -File tests/acceptance/plan3_windows.ps1
```

It creates a disposable project under a unique temporary root and refuses to run against a production checkout.
Before it builds or runs anything it points `CFO_HOME` at the disposable home under that root and `CFO_STATE_OVERRIDE` at that home's `state` directory, so a shell that already exports a fleet home cannot hand the nested `go test` run or the real `cfo` binary the running fleet, and it restores both afterwards.

## Conventions

- Table-driven tests for parsers, classifiers, flag mapping, and state transitions.
- Typed errors that preserve the failed operation, target, and external stderr.
- One sentence per line in Markdown.
- No agent names as commit co-authors.

## Questions

Open an issue.
