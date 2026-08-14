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
```

CI runs the same three steps on `windows-latest` for every push to `main` and every pull request.
A pull request must keep all three green.

## Repo layout

- `cmd/cfo/` - the `cfo.exe` entry point and command handlers.
- `internal/` - one package per subsystem: `herdr`, `treehouse`, `spawn`, `fleet`, `monitor`, `wake`, `lock`, `state`, `home`, `watch`, `harness`, `axi`, `execx`, `fsx`, `claudehook`, `digest`, `doctor`, `guard`, `crewstate`, `supervise`, `proc`.
- `docs/superpowers/` - the design spec and implementation plans.
- `tests/acceptance/` - the opt-in real-session Windows acceptance script.
- `AGENTS.md` - the CFO's operating contract; `CLAUDE.md` points to it.

## Tests

Unit tests are deterministic: they inject fake subprocess runners, fake Herdr panes, and scripted clocks instead of requiring installed tools.

```sh
go test ./...
```

The real-session acceptance suite needs real Herdr, treehouse, Claude Code, Codex, and Pi, and is opt-in:

```powershell
$env:CFO_PLAN3_REAL = '1'
powershell -NoProfile -ExecutionPolicy Bypass -File tests/acceptance/plan3_windows.ps1
```

It creates a disposable project under a unique temporary root and refuses to run against a production checkout or shared treehouse pool.

## Conventions

- Table-driven tests for parsers, classifiers, flag mapping, and state transitions.
- Typed errors that preserve the failed operation, target, and external stderr.
- One sentence per line in Markdown.
- No agent names as commit co-authors.

## Questions

Open an issue.
