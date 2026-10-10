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

Every line of its report has the same shape: how bad it is, the area and the check, what was found, the evidence it rests on, and the fix.

```text
high record/record-missing: the project has no record, so nothing steers its routing, verification or deployment | evidence: no file at C:\Users\you\AppData\Local\CodeGoblins\data\projects\northwind\project.json | fix: write that file (docs/project-runtime.md lists its fields)
ok configs/env-files-ignored: git ignores 2 of the project's 2 env files | evidence: git check-ignore names .env, web/.env.local. Examples, which are committed on purpose: .env.example
project northwind: record failed (1 high), gate passed, configs passed, connectors passed, instructions passed
```

The severities are `ok`, `low`, `medium`, `high` and `critical`.
An area passes when none of its lines is worse than `low`, and the command exits 0 when every area asked for passes.
`--area <area>` assesses one area and may be repeated.
`--json` prints the same report for a program, with the drafted record in it.

| Area | What it proves |
| --- | --- |
| `record` | `project.json` is there, the loader takes it, and it names the project it is filed under. Each command of its verification and security tiers is a program this machine has. A record with no verification command is reported, because `cfo verify` passes with nothing run. |
| `gate` | `.no-mistakes.yaml` is on the default branch, and each command it names has its program, its script files and its package script. Its test step is the repository's own command, since without one an agent chooses what runs. The repository has a workflow for the gate's ci step to wait for, and CI runs the test runners the gate's test command starts. No env file a goblin's worktree shares holds a production value the test setup does not name. |
| `configs` | Git ignores every env file of the project except the examples it commits on purpose. A credential in a file git does not ignore is critical, and the report names its variable and never its value. `worktree.json` shares files the checkout holds and installs with programs and files that exist. `services.json` names a compose file the project has, services that file declares, an env file the checkout holds and a check that can run. |
| `connectors` | Every service `auth.json` declares is read somewhere in the repository outside documents and tests. Every credential the code reads from the environment, an env example names or an MCP connector in `.mcp.json` authenticates with is declared by a service. |
| `instructions` | Each build, test and lint command `AGENTS.md` and `CLAUDE.md` name has its program, its files and its package script. The ones that are there are listed with their kind for whoever runs or dry-runs them. A deploy, a publish and a migration are listed apart and never run. |

Tracked files are read at the default branch as the checkout last fetched it, never from the folder.
A checkout that lags its remote or sits on another branch would otherwise answer for a repository that no goblin's worktree is cut from.
Each line names the commit it read.

The connectors area reads names only.
`cfo auth <project> --check` is the command that asks each service whether it answers, and its sign-in request names what only the operator can supply.

### A test run that can reach production

A goblin's worktree is given its own read-only copies of env files from the checkout: the ones `worktree.json` lists as `link`, or `.env`, `.env.local` and `.env.docker.local` when it lists none.
Whatever those files hold is what a test run in the worktree starts with.
The gate area reads them, and the env files the repository tracks, and counts a variable as a production value when it is one of these:

- a live secret key, by its prefix
- an environment selector such as `APP_ENV` set to production
- a URL of a data store, a queue or a reporting sink whose host is not this machine
- a variable named like a credential whose value is shaped like one and is no test key, publishable key or placeholder

A production value is left out when a test setup file names its variable: a `conftest.py`, a runner's setup or config file, or a script of the gate's own test command.
That is how a project pins what its tests may see.
What is left is reported as `test-reaches-production`, by variable and reason, never by value or host.
The line is `critical` when a live key or a production environment is among them, or when the gate names no test command, since an agent then chooses what the test step runs.
Otherwise it is `high`.

The check reads names, so a setup that clears variables by a rule is not seen and its variables stay on the line.
The lasting fix is on the fleet's side: name a development env file, or none, as `link` in `worktree.json`, so no worktree holds production at all.

### Drafting a record

`cfo project check <project> --draft <file>` writes the record the run can vouch for.
It starts from the record that is there, so nothing a person set is drafted away, and fills only what that leaves empty:

- `services` from the services `auth.json` declares and the repository reads, with credential names and no value
- `verification.fast` from the gate's test command, when that command can run as written

It leaves `verification.full` and `deployment` alone.
A command an instruction file names is prose until someone runs it, and a wrong deploy command is one `cfo deploy` would run.
A draft is never written over a file, and never under the home's `data/projects`, where a record steers routing and verification for live spawns.
A person reads the draft, completes it and places it.

### The project-check skill

The command proves what can be proved without starting anything.
The `project-check` skill is the rest of the pass, one text for every harness, installed with the other skills Code Goblins ships.
It runs `cfo project check` and `cfo auth <project> --check`, then proves each listed build, test and lint command by running it or its dry form.
It runs no deploy, no migration and no test while the report holds a `test-reaches-production` line.
It completes the draft from evidence, proves it in a scratch home with `--area record`, and writes a report with one line for each finding.
A fix in the home's files is the CFO's to place.
A fix inside the project's repository is written up with its proposed text for a follow-up task, which ships through that repository's own gate.
