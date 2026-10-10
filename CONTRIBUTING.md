# Contributing

Thanks for wanting to contribute to code-goblins.

## Workflow

1. Fork [`fpresta0607/code-goblins`](https://github.com/fpresta0607/code-goblins) and clone your fork.
2. Create a branch and make your changes.
3. Run the checks below.
4. Commit with a conventional message - the repo uses `feat(scope):`, `fix(scope):`, `docs(scope):`, and `test(scope):`.
5. Push your branch and open a pull request against `main`.

## Checks

A source build runs `npm ci` and `npm run build` in `frontend` before `go build`: `cfo.exe` embeds the board they build, and one built without it serves a page saying the board was not built.
See [Development](README.md#development) for the requirements before installing a source build.
`go vet` and `go test` need Go alone.
The build lands in `internal/boardweb/dist/board`, which git ignores: a board change commits its source, never its build.

```sh
cd frontend
npm ci
npm run build
cd ..
go run ./cmd/cfo gate test
go build ./cmd/cfo
```

`cfo gate test` vets every package your change reaches and tests the changed packages that are quick to test, naming each with why; `--plan` shows that plan without running it.
It leaves the slow packages' tests, and the packages that only import what you changed, to CI, which runs every package on every pull request: on a Windows machine the whole suite takes an hour or more, where CI's parallel jobs take about eight minutes.
In a slow package, run the tests you changed by name, such as `go test ./internal/supervisor -run 'TestName' -count=1`.
`go test ./...` still runs everything here when you want it.

CI runs the same steps on `windows-latest` for every push to `main` and every pull request, as parallel jobs: the frontend checks, the board's browser tests in four jobs, each slow Go package (some in two jobs), and every other package together.
A pull request must keep all of them green: the one required check, `test`, passes only when every job passed.
A test that fails in CI runs once more in its job (`tools/citest` for Go, Playwright's own retry for the browser tests): one that passes then is named as a Failed once warning on the `test` check, and one that fails twice fails the job, so fix a test named there rather than leaning on its second try.
A new package needs no change to `.github/workflows/go.yml`, because the `rest` job tests every package no other job names.
A new browser spec needs none either: Playwright deals the spec files out among the browser jobs, and one more number in that job's `shard` list is one more job when they grow slower than the slowest Go job.

`cmd/cfo/winres.json` is the Windows version resource and manifest every build of `cfo.exe` carries, through the `rsrc_windows_*.syso` files beside it.
After changing it, regenerate them in `cmd/cfo` with `go run github.com/tc-hib/go-winres@v0.3.3 make --in winres.json --arch amd64,arm64` and commit them; CI fails when they differ, and a release stamps its own version into them.
Build release and deployment binaries with `-trimpath`, so a commit builds to the same file wherever it is built.

## Repo layout

See [Repo layout](README.md#repo-layout) in the README.

## Tests

Unit tests are deterministic: they inject fake subprocess runners and scripted clocks instead of requiring installed tools.
The telemetry and pipeline database regressions are the exception - they build real SQLite fixtures through the `sqlite3` CLI and skip themselves when it is not on PATH, so install it locally to run them (CI puts the release pinned in `.github/workflows/go.yml` on PATH before the suite, from the Actions cache, and asks no package feed for it).

A test must never resolve the fleet home its shell exported.
`internal/home.Resolve` refuses the `CFO_HOME` and `CFO_STATE_OVERRIDE` values the process was launched with whenever the caller is a test binary, so a test that needs a home points both at its own directory.
That refusal cannot cover the real `cfo` binary, which is not a test binary, so a test that execs it - or a shell that resolves it - builds its environment through `cfoTestEnv` in `cmd/cfo`, and the exec helpers fail a test that would hand the child the inherited fleet.
The machine's projects root is a second inherited value with no such refusal behind it, so a test package that builds a production `reap.Collector` or `watch.Config` pins `CFO_PROJECTS_ROOT` at an empty temporary directory in its own `TestMain`, as `cmd/cfo` and `internal/watch` do.
The process value answers before the user scope, so the pin keeps the package off this machine's registry and off whichever checkouts its operator keeps.

```sh
go test ./...
```

### Tests that start a program

A test that needs some program to start uses the one stand-in program, `internal/standin/program`: `standin.Put` writes it to a path and `standin.Bytes` gives its content to serve or pack.
`standin.Env` says what it does for a command: record it, print something, exit with a code, keep running, or copy itself into a home as an install does.
It is built once for each Go toolchain into `cfo\standin` in the user's cache folder and reused by every test run from every checkout, and two builds of it are the same bytes.

A test never serves or runs a copy of its own test binary where the stand-in would do.
A test binary is a new unsigned program at every build, and Microsoft Defender asks its cloud about a program it has never met before it lets it start: 0.7 to 5 s on the Overlord's PC on 2026-10-10, and 39 s under load, which is past a launch deadline.
The install tests used to serve such a copy as the `cfo.exe` that `install.ps1` downloads, renames and runs, which is what a dropper does, and Defender's verdict on those bytes then named the test binary itself, 22 times in two weeks.
No stand-in is named after a program Windows ships, such as `rundll32.exe`, since a file with such a name outside Windows' own folders is how malware hides.
`internal/standin`'s tests fail on a new copy of a test binary and on such a name, and list the test files that still make a copy for a stand-in that runs their package's own code.
A test that only needs bytes it never runs uses bytes that are no program.

The real-session acceptance suite needs real Herdr, Claude Code, Codex, and Pi, and is opt-in:

```powershell
$env:CFO_PLAN3_REAL = '1'
powershell -NoProfile -ExecutionPolicy Bypass -File tests/acceptance/plan3_windows.ps1
```

It creates a disposable project under a unique temporary root and refuses to run against a production checkout.
Before it builds or runs anything it points `CFO_HOME` at the disposable home under that root and `CFO_STATE_OVERRIDE` at that home's `state` directory, so a shell that already exports a fleet home cannot hand the nested `go test` run or the real `cfo` binary the running fleet, and it restores both afterwards.

## Making a release

A release is a tag: `vX.Y.Z` pushed on `main`.
`release.yml` builds `cfo.exe`, `goblins-window.exe` and `CodeGoblinsSetup.exe` from it, scans them with Microsoft Defender, writes `SHA256SUMS`, pins `install.ps1` to the tag, and leaves a draft release; it never publishes by itself.
It signs the three programs when the `release` environment holds the whole signing identity, says in the draft's notes that the release is unsigned when it holds none, and stops when it holds only part of it.

Before the tag:

- `go run ./tools/notices -check` passes after `npm ci` in `frontend`, so `THIRD_PARTY_NOTICES` lists what the release ships.

Before publishing the draft:

- Its notes say signed or unsigned as intended, and list a SHA-256 for each program.
- `defender-scan.txt` shows Defender actively protecting, with nothing excluded and no detection.
- The draft's `install.ps1`, run on a clean machine or in the install workflow, installs the three programs, and opening the app shows the board.

## Conventions

- Table-driven tests for parsers, classifiers, flag mapping, and state transitions.
- Typed errors that preserve the failed operation, target, and external stderr.
- Start every process with `execx.Command` or `execx.CommandContext`, and add creation flags with `|=` rather than assigning them.
  On Windows they keep a console program from opening a window when the program starting it has no console of its own, and `internal/execx`'s tests fail on any process start in fleet code that goes around them.
- Read fleet files with `fsx.ReadFile` or `fsx.Open`, append to them with `fsx.OpenAppend`, and replace them with `fsx.AtomicWriteFile`.
  On Windows a reader that does not share the file for deletion, as `os.Open` and `os.ReadFile` do not, blocks every replace of that file while it reads, and `internal/fsx`'s tests fail on any read or append in fleet code that goes around them.
- One sentence per line in Markdown.
- Never name an AI product, company, model, agent, or assistant identity as a commit co-author - not in a `Co-Authored-By` trailer, not anywhere else in a commit message, and not in a pull request body.

## Questions

Open an issue.
