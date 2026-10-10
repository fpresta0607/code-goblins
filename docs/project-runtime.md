# Project runtime contracts

Code Goblins keeps secrets in the existing auth store and keeps infrastructure shape in `data/projects/<project>/project.json`. The runtime manifest is strict JSON: unknown fields fail closed, provider strings are intentionally open-ended, and credential values are never valid manifest data.

```json
{
  "project": "northwind",
  "services": [
    {"name":"postgres","kind":"database","provider":"supabase","environment":"production","location":"remote","env":["DATABASE_URL"],"health":["psql","$DATABASE_URL","-c","select 1"]},
    {"name":"redis","kind":"redis","provider":"upstash","location":"remote","env":["REDIS_URL"]},
    {"name":"vectors","kind":"vector-store","provider":"qdrant","location":"remote","env":["QDRANT_URL","QDRANT_API_KEY"]},
    {"name":"frontend","kind":"frontend","provider":"vercel","environment":"production"},
    {"name":"backend","kind":"backend","provider":"fly","environment":"production","depends_on":["postgres","redis","vectors"]}
  ],
  "deployment": {
    "required": true,
    "targets": [
      {"name":"web","provider":"vercel","command":["vercel","--prod"],"verify":["curl","-f","https://app.example.com/api/health"]},
      {"name":"api","provider":"fly","command":["flyctl","deploy"],"verify":["curl","-f","https://api.example.com/health"]}
    ]
  },
  "verification": {
    "fast":[["go","test","./internal/foo/..."]],
    "full":[["go","test","./..."]],
    "changed_only":true,
    "max_fast_seconds":90,
    "max_full_seconds":900
  },
  "security": {
    "mode":"risk",
    "fast":[["gitleaks","detect","--no-git"]],
    "deep":[["govulncheck","./..."]],
    "triggers":["auth","payments","migration","infra"]
  },
  "hygiene":{"supersede_clean":true},
  "routing": {
    "default_lane":"open-builder",
    "escalate_to":"subscription-rescue",
    "lanes": {
      "open-scout":{"harness":"pi","model":"openrouter/your-preset","effort":"low"},
      "open-builder":{"harness":"pi","model":"your-open-model","effort":"medium"},
      "subscription-rescue":{"harness":"codex","effort":"high"}
    }
  },
  "budgets": {
    "scout":{"warn_context_tokens":30000,"compact_context_tokens":50000,"restart_context_tokens":80000,"max_repair_rounds":1},
    "builder":{"warn_context_tokens":50000,"compact_context_tokens":75000,"restart_context_tokens":110000,"max_repair_rounds":2}
  }
}
```

`cfo spawn` without `--harness` deterministically classifies the task and selects a lane: from the manifest's `routing` block when it defines lanes, otherwise from the fleet table in `data/routing.json` (`cfo doctor` prints it). A lane whose provider or model scope quota-axi reports `exhausted_now` is passed over for the next usable one, and a spawn with no usable lane is refused with the reset time; a missing, stale, or unparseable quota-axi is no evidence. Explicit `--harness`, `--model`, and `--effort` remain authoritative overrides, and `--auto` is an alias for the default. A project without a manifest routes from the fleet table and gets no capsule. Spawn writes `runtime-capsule.md` and `task-capsule.json` under the task temporary directory; both contain names and policy, never secret values.

Verification is tiered. `cfo verify <task> --tier fast` is the default changed-scope gate; `full` is the project regression gate; `deep` is for expensive validation. Every command produces structured timing, exit, scope, task, and commit evidence. `cfo security` follows the project security mode. `cfo deploy` executes every declared target and its verification command, so GitHub CI cannot substitute for a required Vercel/Fly/Railway/custom deployment.

When a direction is rejected, `cfo supersede <task> --reason ...` records a durable supersede event, writes cleanup instructions, and steers the worker to remove unshipped debris before continuing. `cfo hygiene <task>` reports suspicious test debris without deleting it.

## Checking a project

`cfo project check <project>` says whether what the home knows about a project is still true.
It reads the checkout and the project's files under `data/projects/<project>/` and changes neither.
It starts no command of the project and probes no service.
The one thing it asks outside this machine is where the remote's default branch is, with `git ls-remote`, which writes nothing to the repository.

Every line of its report has the same shape: how bad it is, the area and the check, what was found, the evidence it rests on, and the fix.

```text
high record/record-missing: the project has no record, so nothing steers its routing, verification or deployment | evidence: no file at C:\Users\you\AppData\Local\CodeGoblins\data\projects\northwind\project.json | fix: write that file (docs/project-runtime.md lists its fields)
ok configs/env-files-ignored: git ignores 2 of the project's 2 env files | evidence: git check-ignore names .env, web/.env.local. Examples, which are committed on purpose: .env.example
project northwind: checkout passed, record failed (1 high), gate passed, configs passed, connectors passed, instructions passed
```

The severities are `ok`, `low`, `medium`, `high` and `critical`.
An area passes when none of its lines is worse than `low`, and the command exits 0 when every area asked for passes.
`--area <area>` assesses one area and may be repeated.
`--json` prints the same report for a program, with the drafted record in it.

| Area | What it proves |
| --- | --- |
| `checkout` | This machine has a checkout of the project. The line says which branch and commit were read and when that branch was last fetched, and whether the remote's default branch is still at that commit. |
| `record` | `project.json` is there, the loader takes it, and it names the project it is filed under. Each command of its verification and security tiers is a program this machine has. A record with no verification command is reported, because `cfo verify` passes with nothing run. |
| `gate` | `.no-mistakes.yaml` is on the default branch, a gate's start takes it under the home's pipeline policy, and each command it names has its program, its script files and its package script. Its test step is the repository's own command, since without one an agent chooses what runs. The repository has a workflow for the gate's ci step to wait for, and CI runs the test runners the gate's test command starts. No env file a goblin's worktree shares holds a production value the test setup does not name, and no credential a task's terminal carries is read by the repository and left unnamed by the test setup. |
| `configs` | Git ignores every env file of the project except the examples it commits on purpose. What an env file holds decides its line, not what it is called. A credential or a production value in a file git does not ignore is critical, and the report names its variable and never its value. A tracked env file that holds none, such as a demo setup committed on purpose, is a `low` line, `env-file-committed`, and nothing tells the reader to rotate. `worktree.json` shares files the checkout holds and installs with programs and files that exist. `services.json` names a compose file the project has, services that file declares, an env file the checkout holds and a check that can run. |
| `connectors` | Every service `auth.json` declares has a user the check can name. Every credential the code reads from the environment, an env example names or an MCP connector in `.mcp.json` authenticates with is declared by a service. |
| `instructions` | Each build, test and lint command `AGENTS.md` and `CLAUDE.md` name has its program, its files and its package script. The ones that are there are listed with their kind for whoever runs or dry-runs them. A deploy, a publish and a migration are listed apart and never run. An install is listed apart too, since a worktree's own install step does it. |

Tracked files are read at the default branch as the checkout last fetched it, never from the folder.
A checkout that lags its remote or sits on another branch would otherwise answer for a repository that no goblin's worktree is cut from.
Each line names the commit it read.

### How old the reading is

Every line that says what it read at the default branch also says when that branch was last fetched, and how long before the run that was.
The time is the last fetch that named the branch, which git keeps as `FETCH_HEAD`, or else the last time the branch moved in this checkout.
The check then asks the remote where its default branch is.
A spawn fetches before it cuts a worktree, so a remote that has moved gives a goblin files the check did not read.
That is `remote-moved`, a `medium` line, and its fix is a `git fetch` in the checkout and a second run.
The check itself never fetches.
A remote that does not answer within 20 seconds, or wants a sign-in, is `remote-unanswered`, a `low` line.
What git writes when it fails is never printed, since it can hold the remote's address.
A checkout that names no default branch of a remote has nothing to compare with, and the folder's own commit is read.

### A project with no checkout

A project the home holds a folder for and this machine has no checkout of is a finding, `checkout-missing`, and not an error.
So is a folder that is no git checkout, such as a leftover folder inside another repository, where git would otherwise answer for the repository around it.
What needs no repository is still read: the record whole, and whether the loaders take `worktree.json`, `services.json` and `auth.json` and whether each names this project.
The verdict says how much of each area was read: `gate not assessed`, `configs passed (the home's files alone)`.
An area that was not assessed never passes, so `--area gate` exits 1 for such a project and `--area record` can exit 0.

### Letter case

On Windows a folder answers to its name in any letter case.
The project is named as its checkout's folder is spelled on disk, however the path was typed.
The names inside `project.json`, `worktree.json` and `auth.json` are compared with it without regard to case on Windows, and exactly elsewhere.
One that names another project is a `medium` line in its area.

### What a command needs

A command of the gate file, of the record, of an instruction file and of a worktree's install runs in a worktree, which is cut from the default branch.
So the default branch answers for every file, folder and package script such a command names.
A file the default branch gained since the folder was last pulled is there.
A copy that only the branch the folder sits on tracks is not, and the line says so.
The check of `services.json` is the one command that runs in the checkout itself, so the folder's files answer for it.

A file git does not track is in a worktree only in two cases, and what the checkout's own folder happens to hold does not answer for it:

- `worktree.json` gives it to the worktree: a path under `link`, or under `dependencies.paths` with the `link` strategy. The folder is then read, since the worktree holds what the folder holds.
- The worktree's install step makes it: a path under `.venv`, `venv` or `node_modules`, when an install command of `worktree.json`, or the one the lockfile at the default branch names, makes that folder.

So `.venv/Scripts/python.exe` is found where the worktree's install is `uv venv` and reported where no install step makes a virtual environment, whether or not the Overlord's own folder has one.
The check reads the install the way a spawn does, through the same function, so the two cannot disagree.
The record's tiers are judged by the same rule, since `cfo verify` runs them in a worktree.

What a command writes does not have to exist before it runs.
The check passes over the target of `-o`, of a flag named for output such as `--outfile`, and of a redirect, the destination of `cp` and `mv`, and everything `mkdir`, `touch`, `tee` and `rm` name.
What the same command reads is still judged.

An instruction file is prose, and prose shows the shape of a command with a name its writer made up, such as "run one test file with `npx tsx --test tests/someFile.test.ts`".
A path counts as made up only when both of these hold:

- its file name holds a placeholder word: some, example, sample, foo, bar, baz, qux, your, my, placeholder, xxx or xyz
- no commit the default branch can reach ever held the path

The second is what keeps a file that is really missing reported: one that was deleted, renamed or moved was once held, whatever it is called.
A folder above the file is never asked, so a missing file under `example/` is still a `high` line.
In a shallow clone the history cannot say, so there every missing path is reported.
A command with a made-up path is not dropped.
It is listed on its own line, `instruction-example-paths`, with its file and line.
The rule is for instruction files only: a made-up path in the gate file is a fault.

### Services and credentials

The connectors area reads names only.
`cfo auth <project> --check` is the command that asks each service whether it answers, and its sign-in request names what only the operator can supply.

A declared service counts as used by one rule, and the draft keeps a service by the same rule:

- a tracked file outside documents and tests reads one of its variables, or
- its own entry in `auth.json` says what uses it outside the repository: a method of `cli`, or a probe, a login or an identity command, whose program is the tool, or
- its entry has a note

The fleet pushes with `gh` in every project and no repository names `GITHUB_TOKEN`, so reading the repository alone can never show that a service is unused.
The goblin that types a command is a reader too.
`connector-unused` is therefore only a service that nothing reads and whose entry says nothing.
The line `connectors-examined` names each service used through a tool, with whether this machine has the tool, and each kept on a note alone, which the check cannot verify.

`connector-undeclared` leaves out three kinds of name, since the fleet supplies none of them:

- a harness's own billing key, which no manifest may inject
- a name only a file under `.github` reads, where GitHub supplies it
- a publishable name

The last two are named on the `connectors-examined` line, so what was left out is seen.

### A test run that can reach production

A goblin's worktree is given its own read-only copies of the env files `worktree.json` lists as `link`, and of no other: with no `worktree.json`, or with one that names no `link`, it is given none.
Whatever those files hold is what a test run in the worktree starts with.
The gate area reads them, and the env files the repository tracks, and counts a variable as a production value when it is one of these:

- a live secret key, by its prefix
- an environment selector such as `APP_ENV` set to production
- a URL of a data store, a queue or a reporting sink whose host is not this machine
- a variable named like a credential whose value is shaped like one and is no test key, publishable key or placeholder

A publishable key is handed to every browser by design, so it is no credential.
The value is asked first, since it can say what a name cannot.
A key that starts the way a secret one does, or whose own payload names a role other than `anon`, is a credential whatever variable holds it.
A key whose payload names the role `anon`, or that starts `pk_`, `pk.` or `sb_publishable_`, is publishable.
Where the value says nothing the name decides: `PUBLIC` or `PUBLISHABLE` anywhere in it, the word `ANON`, or a prefix a bundler ships to the browser, `VITE_`, `REACT_APP_` or `GATSBY_`.

An env template is read as an example under any of its usual spellings: `.env.example`, `env.template`, `.env-example`, `example.env`.

A production value is left out when the test setup names its variable.
That is how a project pins what its tests may see.
What is left is reported as `test-reaches-production`, by variable and reason, never by value or host.
The line is `critical` when a live key or a production environment is among them, or when the gate names no test command, since an agent then chooses what the test step runs.
Otherwise it is `high`.

The check reads names, so a setup that clears variables by a rule is not seen and its variables stay on the line.
The lasting fix is on the fleet's side: name a development env file, or none, as `link` in `worktree.json`, so no worktree holds production at all.

### What counts as the test setup

The test setup is every place the check reads for a variable a project pinned for its tests:

- a file a test runner loads before the tests, by its name: `conftest.py`, `pytest.ini`, `tox.ini`, `.env.test`, a setup or config file of jest, vitest, playwright, mocha or cypress, `phpunit.xml`, and the helper files of rspec and minitest
- `pyproject.toml` and `setup.cfg`, when they hold a pytest section
- each test command: the gate's own, or where the gate names none, the test commands the workflows run and the instruction files name, since an agent that chooses the test step chooses among those

A test command is followed through the package script it runs, three deep at most, to the commands that script comes to.
Of each the check reads three things:

- the script it hands an interpreter to run, such as `scripts/gate_test.py` in `uv run scripts/gate_test.py`
- the files it loads before the tests with `--import`, `--require` or `-r`
- the text of the command and of the scripts themselves, since a script can set a variable in front of what it runs

A test file a command names is no setup.
One that reads a variable has not pinned it, so what follows the script on its line, and a file handed to a test runner, to `-m` or to `--test`, is not read.

A suite of `node --test`, `tsx --test`, `go test`, `cargo test`, `dotnet test` or unittest has no setup file: each test file sets what it needs, in a process or a package of its own, so nothing pins a variable for a whole run.
The line says so by the command that starts it, in place of "none this check knows", which is kept for a test command the check can make nothing of.

### What a task's terminal carries

An env file is one of two roads a test run has to production.
The other needs no file: a spawn writes the stored credentials of a task's services into the task's terminal, and a test run started there inherits them.
`link` in `worktree.json` does not touch that road.

Which services a task carries is the auth manifest's own answer, asked through the function a spawn uses:

- a task whose brief has no `credentials:` line carries the services `auth.json` marks `default`, and no other
- a task whose brief names services carries exactly those

The gate area reads `auth.json` and names, for each service, the variables a terminal would carry.
A variable counts as within a test run's reach when both of these hold:

- a tracked file of source code names it as a word of its own, in any letter case: application code or a test, and not a document, a settings file, an env file or a file under `.github`, where GitHub supplies the value
- no place of the test setup names it

A publishable name is left out, as on the env file line, and so is a variable named for tests, such as `TEST_DATABASE_URL`: one of its words is `TEST` or `TESTING`.
Both are named on the line that says what was examined.

What is left is `terminal-reaches-production`, with each variable beside the first tracked file that reads it.
It is `high` when a default service carries one, since every task with no credentials line then does, and `critical` when the gate names no test command as well.
It is `medium` when only a service a brief has to name carries one.
The fix is in the repository, a test setup that names each variable with a value for tests, or in the home, taking `default` off a service or keeping it off the credentials line of a task that runs the tests.

The line `terminal-credentials-examined` says what was read either way: each service with its variables, which every task carries by default, which are read by no tracked file, which were left out as publishable, and which services are optional.
The check reads `auth.json` and the repository and never opens the credential store.
So it names variables, and cannot say whether one is stored or whether what it holds is a test value: a terminal carries a variable only when the store holds it.

### Drafting a record

`cfo project check <project> --draft <file>` writes the record the run can vouch for.
It starts from the record that is there, so nothing a person set is drafted away, and fills only what that leaves empty:

- `services` from the services `auth.json` declares and the check counts as used, with credential names and no value
- `verification.fast` from the gate's test command, when that command can run as written, one command for each part of it

A draft has to pass the check that wrote it, and a record with no verification command fails the record area.
So for a record with none, where the gate names no test command a record can hold, the fast tier is filled from the next place the repository names its tests:

1. the test commands its workflows run at the repository's root, each a plain command that can run as written
2. the test commands its instruction files name that can run as written at the root

The check runs none of them.
The command prints where the tier came from on a `draft tier:` line, and `--json` carries the same sentence as `draft_tier`.
Prove each command before the draft is placed.
Where the repository names no test command anywhere there is none to vouch for, so no draft is written, and the command says why and exits 1.

It leaves `verification.full` and `deployment` alone.
A wrong deploy command is one `cfo deploy` would run.
A draft is never written over a file, and never under the home's `data/projects`, where a record steers routing and verification for live spawns.
A person reads the draft, completes it and places it.

### What a gate's start asks of the gate file

`cfo pipeline run` asks three things of a repository's gate file before it starts a gate run, and refuses the run when one fails.
The check asks the same three, each through the pipeline's own code and against the home's policy, `config/pipeline.json`, so every answer is the pipeline's verdict and not a second copy of its rule.

- **The file is there.** A repository with no `.no-mistakes.yaml` at its default branch is refused at the start of every gate run. That is `gate-file-missing`, a `high` line, and it quotes the pipeline's answer. A gate started any other way leaves every step to an agent's choice.
- **Its automatic fix counts are within the policy's.** A gate file may lower how often a gate repairs a step by itself and may never raise it. A count above the policy's, a count the policy does not govern, and a file the pipeline's reader does not take, such as one with YAML anchors, are `gate-file-refused`, a `high` line. Counts the policy takes are `gate-limits-read`, an `ok` line.
- **The agent it pins is one the policy takes.** Before policy version 6 a pin other than the policy's own gate agent is refused: `gate-agent-refused`, a `high` line. From version 6 a run's own launch selection replaces a repository's agent, so a pin decides nothing and the line is `ok`.

With no policy file the check can read, a count is `gate-limits-unjudged` and a pin is `gate-agent-unjudged`, both `low` lines.
A gate file that sets no count and pins no agent, and that the pipeline's reader takes, has no line.

The start also asks things of the task and not of the project, which the check does not read: that the task's work is committed, that no earlier run of its branch is unresolved, and that its own branch still holds the gate file.
It asks too that the checkout's copy of the default branch is the remote's, which is what `remote-moved` reports.

### What the check cannot see

The check says only what it read, and these are outside it today:

- What the credential store holds. The terminal line names the variables `auth.json` declares and never opens the store, so it cannot say whether one is stored or whether its value is a test one.
- What the user's own environment sets. A task's terminal starts from it, less the variables of services the task does not carry, so a credential set there reaches every terminal and no line names it.
- A credential only a program reads. A test that starts `gh` uses `GITHUB_TOKEN` though no tracked file names it, and such a variable is listed as read by no tracked file.
- Production values its rules do not know: a short password, a name ending in a word it does not take for a credential, an address whose name holds no service word. Every count of production values in an env file is a floor.
- What really loads an env file. It counts a variable as within a test's reach whenever the file is in the worktree, and does not read whether a test runner, a settings loader or a script loads it.
- A test setup that pins a variable by a rule and not by name, and a test command the check can make nothing of.
- A checkout below another folder of the projects root. `--unfiled` looks one folder down.
- A program an install step makes outside `.venv`, `venv` and `node_modules`, such as a binary a build writes to `bin`. It is reported as not in a worktree.
- Whether the remote has moved, when the remote does not answer.

### Checkouts the home holds no folder for

`cfo project check --unfiled` takes no project.
It names each checkout directly under the machine's projects root that the home holds no folder for under `data/projects`, one `ok` line each, `checkout-unfiled`.
A spawn can be sent into any of them, and the line says what it would be given there: no env file, since a worktree is given one only when a `worktree.json` names it, no credential from the credential store, since a project with no `auth.json` has no service to grant, and no record to steer its routing or its verification.
It names the env files the checkout holds at its root, which stay in the checkout, and opens none of them.
A last line, `checkouts-examined`, counts the checkouts it looked at and names the home's folders that have no checkout directly under the root, which may be kept elsewhere.
A folder counts as a checkout when it holds a `.git`.
The command exits 0 whatever it names, since a checkout the home knows nothing of is no fault.
To file one, run `cfo project check <its path>` and place what it drafts.

### The project-check skill

The command proves what can be proved without starting anything.
The `project-check` skill is the rest of the pass, one text for every harness, installed with the other skills Code Goblins ships.
It runs `cfo project check` and `cfo auth <project> --check`, then proves each listed build, test and lint command by running it or its dry form.
It runs no deploy, no migration and no test while the report holds a `test-reaches-production` line.
It completes the draft from evidence, proves it in a scratch home with `--area record`, and writes a report with one line for each finding.
A fix in the home's files is the CFO's to place.
A fix inside the project's repository is written up with its proposed text for a follow-up task, which ships through that repository's own gate.
