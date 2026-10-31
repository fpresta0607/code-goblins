## Project worktree environment

Every goblin works in a git worktree of the project at `$CFO_HOME\worktrees\<project folder>\<id>`, detached from the project's default branch, and an extra worktree it asks for with `cfo worktree add` sits beside it.
cfo's own git commands there turn `core.longpaths` on, because a worktree under the home is a few folders deeper than one inside the checkout; `cfo doctor` says whether it is on for the goblin's own git.
A worktree starts as the files git tracks and nothing else of the checkout.
An env file holds the project's production values, so none reaches a worktree by silence: with no worktree manifest, or with one that names no `link`, a goblin's worktree is given no file of the checkout.
A spawn says so when it matters, by file name only: ``env: the checkout holds .env, .env.local, which this worktree was not given``, with the `link` line that shares them, and it tells the goblin the same, so nobody learns it from a dev server that will not start.
A project can declare how that worktree becomes runnable in `data/projects/<name>/worktree.json`, beside its auth manifest:

```json
{
  "project": "northwind",
  "link": [".env", ".env.local", ".env.docker.local"],
  "dependencies": {
    "strategy": "install",
    "install": ["uv venv", "uv pip sync --require-hashes requirements.lock"]
  },
  "env": { "PLAYWRIGHT_BROWSERS_PATH": "C:\\cache\\ms-playwright" }
}
```

- `link` names root-level config files or directories the worktree is given from the primary checkout: a file as the worktree's own read-only copy, made when the worktree is, so a goblin's edit never reaches the checkout's file and a later edit to the checkout's file reaches only worktrees made after it, and a directory by junction.
  Nothing is shared that it does not name, and a missing source is skipped.
  Name a development env file there, never one that holds production values.
  An entry whose path the worktree already holds (a project that commits `.env`) is refused, because the manifest asked for it to be shared and the repository's own file is in its place.
- `dependencies.strategy` is `install` (the default: the goblin runs the installer the lockfile implies - pnpm, npm, yarn, or uv - in its own terminal as its first step, against the shared cache root below), `link` (junction the declared `paths` from the primary checkout; instant and zero disk, but a package-manager run in one worktree mutates them all), or `none`.
- `dependencies.install` overrides the detected install commands; each entry is one command line the goblin runs in order in its worktree, stopping at the first that fails.
  Each must install exactly what a lock with hashes holds, with nothing chained after it: `npm ci`, `pnpm install --frozen-lockfile`, `yarn install --frozen-lockfile` or `--immutable`, `uv sync --locked` or `--frozen`, and `uv pip sync` or `uv pip install` with `--require-hashes`, beside `uv venv` (without `--seed`) and `python -m venv` (without `--upgrade-deps`), which make an empty environment.
  One that resolves versions at a package index, such as `uv pip install -r requirements.txt`, gives the goblin no install step at all: on 2026-10-21 that command brought a hijacked release to three goblins within ten minutes of its upload.
  A `package.json`, `pyproject.toml` or `requirements.txt` at the worktree's root with no lockfile of its own that the strategy installs from is installed by no step either, beside the install of a lockfile the worktree does hold, such as a `requirements.txt` beside a `package-lock.json`, and a Go module, whose `go.sum` holds its hashes, needs none.
  `cfo project check` reports a manifest install that resolves as `worktree-install-unlocked`; a requirements file compiled with `uv pip compile --generate-hashes` is a lock with hashes.
  The spawn runs no installer: an install writes tens of thousands of files, which under on-access scanning took one spawn over 30 minutes while it held the home's spawn lock, so every other start and resume waited behind it.
  In the goblin's own terminal an install holds up only that goblin, its output is on the goblin's screen, and the goblin is the one that can repair a lockfile it fails on.
- `env` carries environment redirects for large read-only caches into the goblin's pane, where its dependency install runs, and overrides the machine-wide cache redirects below for this project.
- Everything provisioning places inside the worktree, and what the install commands will create there (`node_modules`, `.venv`), is registered in the clone's `info/exclude` when the project does not already ignore it, so the goblin's `git status` reflects only its own work.
  That matters because `cfo cleanup` refuses a worktree whose status is not empty, so an unignored provisioned artifact would read as uncommitted goblin work and block removal.
  Git has no per-worktree exclude file: `info/exclude` lives in the clone's shared common directory, so those entries - whatever config and dependency paths provisioning places, such as `.env`, `node_modules`, or `.venv` - also apply to the primary checkout, and cleanup does not remove them.
  A project that already ignores them gets no entry at all, and the worktree itself, outside the checkout, needs none.
  Edit `.git/info/exclude` by hand if the primary checkout needs one of them back.
- `cfo cleanup` also refuses while the harness is still attached to the pane - agent state `idle` and `done` both count as active, and there is deliberately no force override.
  A goblin that has filed its terminal `cfo notify` is finished with the work but has not left the pane; release it with `cfo send <id> "/exit"` and confirm with `cfo peek` that the pane is back at a shell prompt before running cleanup.
  That send may report the agent gone and exit non-zero, and here that is not a failure: `/exit` is precisely what deregistered the agent, so nothing is left to confirm the prompt with.
  A clean `sent` and a zero exit are equally normal - the harness had simply not finished exiting when the confirmation read landed, so the agent was still registered and its counters had already moved on the `/exit` turn.
  `cfo peek` is the confirmation either way.
- The goblin receives the token-authenticated subset of the project's `.mcp.json`: stdio servers and HTTP servers with a bearer token qualify; OAuth-only connectors are withheld and named on the `spawned` line, because a goblin can never complete their browser flow.
  A server whose entry holds a value, in a command server's `env` map or in a header, is withheld too unless the task's brief names it, and is named on its own `mcp: withheld ...` line with the command that grants it ([Credentials by need](authentication.md#credentials-by-need)).
  That filtered configuration is materialized under the task's temporary directory, outside the checkout, and it is the only file a harness is handed through `--mcp-config`.
  A kept URL server with no `type` gets the one Claude needs (`sse` for an `/sse` endpoint, otherwise `http`), and an explicit `type` is kept; for any kept URL server without its own `Authorization` header, typed or not, a `bearerTokenEnvVar`, which Claude does not read, becomes its `Authorization` header as an unexpanded `${VARIABLE}` reference.
  A server whose only token is a `bearerTokenEnvVar` set neither as a declared project credential nor in the environment the goblin's native terminal starts with, the user's own, is withheld and named with its variable on the `spawned` line, because Claude would send the reference unexpanded and the server could only fail or ask for authentication.
  A copy is also written to the worktree root for harnesses that read the project-scoped `.mcp.json` from their working directory, but only when that path is free and untracked.
  A project that commits `.mcp.json` keeps its file exactly as committed, and the `spawned` line says so: a working-directory-reading harness then still sees every server declared there, withheld ones included.
  A relaunch writes both files again from the project's `.mcp.json` as it reads then: the worktree's copy is replaced only while it is still the copy the last start wrote, so a `.mcp.json` the goblin wrote there is left.

### Share caches, never share materialized environments

Every goblin's pane inherits one shared package-cache root at `$CFO_HOME/caches/`, with a subdirectory per ecosystem: `UV_CACHE_DIR`, `pnpm_config_store_dir` (pnpm's store, read by pnpm 11 and later), `npm_config_cache`, `PLAYWRIGHT_BROWSERS_PATH`, `GOMODCACHE`, and `GOCACHE`.
The janitor keeps the folder under `config/fleet.json`'s `caches_cap_gb`, default 20, with each tool's own prune (see [The janitor](home.md#the-janitor)).
The goblin runs its dependency install in that pane, against those same redirects, which is where they matter most: the install is both the largest consumer of the store and the thing that fills it.
It also installs from the store the goblin's later commands use, by construction, which matters because pnpm records the store it installed from and a pane pointed at a different one tears `node_modules` down and reinstalls.
`CARGO_HOME` is deliberately excluded and must not be added: cargo has no cache-only variable, so redirecting it would also relocate `config.toml`, `credentials.toml` and `bin/`, and a goblin would lose the operator's registry and linker configuration.
These locations are a property of the machine rather than of any project, so they live in the CFO home and no manifest repeats them.
A variable the CFO's own environment already sets is inherited untouched, and a project's `worktree.json` `env` block wins over both for that project, in the pane and so in the install.
The one exception is the names the launch contract owns - `GOTMPDIR`, `TEMP` and `TMPDIR`, which point into the task's scratch folder, `TMP`, which points to the folder every goblin shares, `CFO_HOME` and `CFO_STATE_OVERRIDE`, `CFO_ROLE`, and `LOCALAPPDATA`, `XDG_CACHE_HOME` and `HOME` - which a manifest cannot redirect in the pane, where the install runs too.
A pane whose cache root moved would leave every `cfo` command run there computing a different Go temporary directory than the spawn that created it.
`cfo auth <project> --env` prints every one of them in full and names where each came from, marking a project-declared one `(project)` and an inherited one `(inherited)`, so a tuned location is visible rather than indistinguishable from one that was never set.
The audit is scoped to the project it is given, so it reports where that project's goblin actually builds rather than the machine default.

A `.venv` or a `node_modules` is never shared or linked between worktrees.
Both bake absolute paths and compiled native artifacts, and a shared one fails as flaky tests rather than as an honest error, so a worktree always re-materializes its own against the shared store.
`PLAYWRIGHT_BROWSERS_PATH` is the shape to prefer wherever it applies: a pure environment redirect to a large read-only artifact with nothing path-baked into what it produces.

### A release a week old, never a fresher one

Every goblin's terminal holds its package managers to releases uploaded at least seven days ago, so a hijacked release is found and pulled before a goblin resolves it: `UV_EXCLUDE_NEWER=7 days`, `PIP_UPLOADED_PRIOR_TO=P7D`, and `npm_config_before` at the date seven days before the terminal started, since npm before 11.10 takes no age.
A spawn and every relaunch set them after the project's `worktree.json` `env` block, so a project that names its own cutoff keeps it, and the goblin is told never to lift them and to ask in a blocked report for a newer release its task needs.
A locked install is not held back by them: `npm ci` and `uv sync --locked` install what the lock names however new it is, while `uv pip` and pip resolve each pin under the cutoff, so a hashed requirements file that pins a release younger than a week fails until it ages.
A worktree that tracks a `uv.lock`, at its root or in a subfolder, gets no `UV_EXCLUDE_NEWER`, and the `spawned` output says so on a `cooldown:` line: uv sets aside a lock that does not record the cutoff its environment names, so `uv sync --locked` would fail and `uv run` would rewrite the lock.
Such a project records its own cutoff as `[tool.uv] exclude-newer = "7 days"` in `pyproject.toml`, which uv keeps in its lock for goblins and CI alike.
A lock a goblin writes under the terminal's cutoff records it too, and a `uv sync --locked` without it, as in CI, fails until the project records the same cutoff.

### Code that runs at every Python start

Every goblin's own hook looks at the Python environments of its worktree before each command it runs: a Claude Code goblin's before each Bash, PowerShell or Monitor call, a Codex goblin's before each shell command and `exec_command` call, and a pi goblin's before each bash call.
It looks in each folder at the worktree's root that holds `pyvenv.cfg`, and the `.venv` of each folder one or two levels below its root, such as `services/api/.venv`, though never under a dot folder or `node_modules`.
An executable line of a `.pth` file in its `site-packages` or, on Windows, in the environment's own folder, where a wheel's data files land, or a `sitecustomize` or `usercustomize` module there or in a folder or zip archive a `.pth` path line adds, found as Python finds it, runs at every Python start whether or not its package is ever imported, which is how the hijacked release of 2026-10-21 ran a stealer that could read everything the Overlord's account can.
The ones an environment carries with no release hijacked pass, each only from what makes it: uv's and virtualenv's own `_virtualenv.pth` in an environment its `pyvenv.cfg` says uv or virtualenv made, the start hooks setuptools, pywin32, coverage and pytest-cov before 7.0 ship from those packages, and any line of a project installed editable from a folder of the worktree, as setuptools, meson-python and scikit-build-core write one.
Any other refuses the call before it runs and every call after it until the file is gone or you accept it, and wakes you, kind `review`, keyed by the goblin, naming the file and the package whose `RECORD` installed it, never what the file says: once while that wake waits for you, and again once you have acknowledged it and the file is still there.
A file no package names yet while the environment's `site-packages` changed within the last minute, as while an install still writes, refuses the call with no wake, and wakes you once the install has ended if no package names it then.
Decide from the package and the file: once you know it is safe, accept it with `cfo start-hook accept <task-id> <file>`, or remove it, and the goblin's commands run again; otherwise end the task.
Accepting is for a file you cannot remove without breaking its package, such as the `-nspkg.pth` file setuptools writes for a package that declares a namespace package, which a project that locks that package installs at every spawn.
It accepts the file by its name, its exact content and the distribution whose `RECORD` installs it, such as `zope.interface-6.4`, for every goblin of the task's project, or of every project with `--fleet`, recorded in `state\accepted-start-hooks.json`.
Their seals let that file through in every later environment, and still stop one whose content, name or distribution differs, as one a later release or another package writes does.
It pins that file alone and no module its lines import, so accept a file whose lines import only the standard library, as a `-nspkg.pth` file's do, or one whose imported module you judged too.
It refuses a file no installed distribution names and a folder, which has no one content, and only you accept: a goblin or a gate agent is refused it.
The command that installed the file may have started Python already, which nothing before a shell call can stop.
Codex runs a Codex goblin's hook only once the Overlord has trusted it in a Codex goblin's `/hooks`, as it runs the goblin's other hooks: until he has, each Codex start says it started without trusting its hooks, and nothing checks before that goblin's commands.
