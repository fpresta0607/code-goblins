# cfo project

`cfo project check <project> [--area <area>] [--json] [--draft <file>]` \| `check --unfiled [--json]` \| `show <project>` \| `init <project>`

Assess what the home knows about one project and say whether it is still true.
It reads the checkout and the project's files in `data/projects/<project>/` and changes neither.
It prints one line for everything it could prove, each with how bad it is (`ok`, `low`, `medium`, `high` or `critical`), the evidence it rests on and the fix, then one verdict line naming each area as passed, failed or not assessed.
The area `checkout` says what was read: the default branch and its commit, when that branch was last fetched, and whether the remote's default branch has moved since, which it asks with `git ls-remote` and never by a fetch.
A project the home holds files for and this machine has no checkout of is a finding there and not an error: the record is still read, the home's other files as far as their loaders go, and the gate and instructions areas are not assessed, which never counts as passed.
On Windows the project is named as its folder is spelled on disk, and the names in its files are compared without regard to letter case.
The area `record` covers `project.json`: it is there, the loader takes it, it names this project, and each command of its verification and security tiers is a program this machine has.
A record with no verification command is reported, since `cfo verify` then has nothing to run and fails the tier.
The area `gate` covers the no-mistakes gate: `.no-mistakes.yaml` is on the default branch, each command it names has its program, its script and its package script, its test step is the repository's own command and not an agent's choice, the repository has a workflow for the ci step to wait for, and CI runs the test runners the gate's test command starts.
What a gate's start asks of the gate file is read against the home's pipeline policy by the code that start asks: that the file is there, that its automatic fix counts are within the policy's, and that the agent it pins is one the policy takes.
Each one the start refuses is a `high` line, since `cfo pipeline run` then refuses every gate run of the project.
It also reads the env files a goblin's worktree shares from the checkout and reports each production value in them: a live secret key, an environment switch set to production, a data store or a reporting sink on another host, an address that carries a login, a real credential, or plain text under the name of a password or a shared secret.
It says what loads each file, as far as a tracked file shows it.
Where a test, its setup or the application's own code loads the file, the values the test setup does not name are `test-reaches-production`, critical when a live key or a production environment is among them or when an agent chooses the test step.
Where no test loads it, they are `worktree-holds-production`, with the scripts, the development server or the local stack that do load it, critical for a live key or a production environment and high otherwise.
Both name variables and why, never a value or a host, and both say the count is a floor: the line that says what was examined names each variable whose value the rules passed over.
The gate area also reads `auth.json` for credentials a test run could inherit from its terminal; [What a task's terminal carries](../project-runtime.md#what-a-tasks-terminal-carries) owns the `terminal-reaches-production` criteria and severity.
It never opens the credential store, so it names variables and cannot say what one holds.
The area `configs` covers the env files, which git must ignore unless they are examples, with a credential or the address of a production store in one it does not ignore reported as critical by its variable and never by its value, and a tracked one that holds none, such as a demo setup committed on purpose, reported as `low` with nothing to rotate, and `worktree.json` and `services.json`, which must name files, programs, compose services and a check the repository has.
The area `connectors` covers `auth.json`: a service it declares that no tracked file reads and whose own entry names no command line tool, by a method of `cli`, a probe or a login, and has no note, which is the one rule the draft keeps a service by, and a credential the code, an env example or an MCP connector in `.mcp.json` reads that no service declares, leaving out a name only a workflow reads, where GitHub supplies it, and a publishable name, both named on the line that says what was examined.
The area `instructions` covers the build, test and lint commands `AGENTS.md` and `CLAUDE.md` name: each has its program, its files and its package script.
It lists the ones that are there for whoever runs or dry-runs them, and lists apart a deploy, as never run, an install, as what a worktree's own install step does, and a command that names a path made up to show its shape, which counts as made up only when its file name holds a placeholder word and no commit of the default branch ever held it.
Tracked files are read at the default branch as the checkout last fetched it, so a checkout that lags or sits on another branch does not answer for the repository, and each line names the commit it read.
A command's files, folders and package scripts are judged there too, since a worktree is cut from the default branch, and what a command writes, such as the target of `go build -o`, is not asked for.
A file git does not track is in a worktree only when `worktree.json` gives it or the worktree's install step makes it, so `.venv/Scripts/python.exe` is judged by that install and not by what the checkout's own folder holds.
A publishable key, by what its value says and then by its name, is no production credential.
It starts no command of the project and probes no service: `cfo auth <project> --check` asks each service whether it answers.
`--area` assesses one area and may be repeated, and `--json` prints the report for a program.
`--draft <file>` writes the record this run can vouch for: the record that is there, with what it leaves empty filled by the declared services the check counts as used and by the gate's test command as the fast tier, one command for each of its parts.
For a record with no verification command, where the gate names no test command, the fast tier is the test commands the workflows run at the repository's root, or else those the instruction files name.
The check runs none of them, and a `draft tier:` line says which it took.
A draft that would name no verification command is not written, since it would fail the record area, and the command says why.
A draft is never written over a file and never under the home's `data/projects`, where a record steers live spawns, so a person places it.
Exit 0 when no line of the areas asked for is worse than `low` and each was assessed, 1 otherwise.
`check --unfiled` takes no project: it names each checkout directly under the projects root that the home holds no folder for, with what a spawn there would be given, which is no env file, no credential and no record, and exits 0.
For the whole pass on a project, load the `project-check` skill (`~/.agents/skills/project-check/`): it runs this command and `cfo auth <project> --check`, proves the listed commands by running or dry-running them and never a deploy, completes the draft, and writes a report with one line for each finding.
`show` prints the record and `init` writes an empty one.
A project is a name or a path for all three
