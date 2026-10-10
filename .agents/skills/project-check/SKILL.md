---
name: project-check
description: Assess what the fleet knows about one project and bring it up to date. Covers its project record, its verification gate, its configs and env files, its services and connectors, and the commands its instruction files name. Use when asked whether a project's gate, configs or connectors are right, before the first dispatch into a project, when a project has no project record, or after a project changed how it builds, tests or deploys.
argument-hint: <project name or checkout path>
---

# project-check

One project in, one report out: what the home knows about the project, whether each fact is still true, the evidence, and the fix.
The checks themselves live in `cfo project check`, so every agent gets the same answer whatever it runs in.
This skill is the part a command cannot do: prove the project's commands by running them safely, judge what the lines mean, and write the result where it belongs.

## What never happens

- Nothing in the project is changed.
  The pass reads the checkout and writes only into its own task folder.
- No deploy, publish or migration is run, and nothing that spends credits or reaches production.
- No secret value is printed, copied or stored.
  A variable is named and its value is not.
- No env file is opened to read it.
  `cfo project check` reads env files and reports names and reasons, which is all a report needs.
- Nothing is written under the home's `data/projects`.
  A record there steers routing and verification for live spawns, so a draft goes to the task folder and the CFO places it.
- No live board, supervisor, CFO or goblin is stopped, restarted or attached to.

## The pass

1. **Assess.**
   Run `cfo project check <project>` and keep its output.
   Each line is a severity, an area and a check, what was found, the evidence, and the fix.
   The last line is the verdict for each area.
   The command exits 1 when an area fails, which is a result and not an error.
   `--json` prints the same report as data, with the drafted record in it.
2. **Ask the services.**
   Run `cfo auth <project> --check`.
   It prints one line for each declared service and ends with one sign-in request, which names what only the Supreme Overlord can supply.
   Copy the names into the report.
   Never run `--fix` in this pass, since it stores credentials.
   A probe asks the real service with the stored credential.
   Where the task forbids reaching production, read the probes in `auth.json` first, skip this step for a project whose probes reach it, and say so in the report.
3. **Prove the commands.**
   The line `instruction-commands-found` lists each build, test and lint command the instruction files name, with its file and line.
   The line `gate-commands-found` lists the gate's commands, and `tier-commands-found` the record's.
   The command proved only that each can run as written, so prove each one here:
   - Prefer the form that changes nothing: a dry run, a plan, a listing or a collect-only run, whichever the program has.
   - Run a build or a lint for real only when it is cheap and writes nothing outside the folder it runs in.
   - Run no test while the report holds a `test-reaches-production` line.
     Record the command as not run and say why.
   - Never run a command from the line `instruction-deploy-never-run`.
     Confirm only that its program is there.
   - Run no command from the line `instruction-install-not-run`.
     A worktree's own install step does that, and an install changes the machine.
   - The line `instruction-example-paths` lists commands that name a path made up to show their shape.
     Read each in its instruction file and say whether it is an example, since the command cannot run as written either way.
   - Run nothing in the Overlord's own checkout that writes to it.
     Use a worktree of the project, or record the command as not run.
   - Mind the machine: one command at a time, the lightest form that proves it.

   For each command record what was run, whether it was the command or its dry form, its exit code and one line of what it printed.
4. **Read what the command cannot.**
   - `test-reaches-production`: open the test setup files and the gate's test command the line names, and say for each variable left on the line whether a rule clears it.
     The check reads names, so a setup that clears variables by a rule is not seen.
     Say what you found and keep the line in the report either way.
   - `connector-undeclared`: for each name say whether the fleet should declare it in `auth.json` or the project loads it from its own env file by design.
   - `connector-unused`: a service is on this line only when no tracked file reads it and its entry in `auth.json` names no tool and has no note.
     Say whether to take it out or what uses it.
   - `connectors-examined` names the services kept on a note alone, which the command cannot verify.
     Read each note and say whether it still holds.
   - `gate-ci-differs` and `gate-ci-unknown`: read the gate's test command beside the workflow files and say what each runs.
5. **Draft the record.**
   Run `cfo project check <project> --draft <task folder>/drafts/<project>/project.json`.
   The draft starts from the record that is there and fills what it leaves empty with the declared services the command counts as used and a fast tier.
   The `draft tier:` line says where the fast tier came from: the gate's test command, or for a record with no verification command the test commands the workflows run, or else those the instruction files name.
   The command ran none of them, so prove each as in step 3 and take out one you could not prove.
   When the command says no draft is written, the repository names no test command anywhere.
   Say so in the report, and propose a `commands.test` for the gate file as a follow-up task.
   Complete the draft from evidence only:
   - `verification.full` from a test command you proved in step 3.
   - `deployment` only from a deploy command the instruction files name and the repository's own deploy manifest confirms.
     Leave `required` false unless the Overlord said a deployment is required.
   - Never a credential's value.

   Then prove the draft in a scratch home, never the live one.
   Set `CFO_HOME` to an empty folder, clear `CFO_STATE_OVERRIDE` and `CFO_PROJECTS_ROOT`, copy the draft to `data/projects/<project>/project.json` under that folder, and run `cfo project check <checkout path> --area record` there.
   It must exit 0.
6. **Report.**
   Write `report.md` in the task folder.
   Give each finding one line: how bad it is, the area and check, what was found, the evidence, and the fix.
   Put the worst first.
   After the findings keep the `ok` lines that say what was examined, since a check that examined nothing is not a pass.
   End with the proofs from step 3 and the verdict line.
7. **Route the fixes.**
   - A fix in the home's files for the project, which are `auth.json`, `worktree.json`, `services.json` and `project.json`, is the CFO's to place.
     Write the proposed text in the report.
   - A fix inside the project's repository, such as its gate file, its instruction files, its ignore file or its test setup, is never made in this pass.
     Write the finding and the proposed text for a follow-up task, which ships through that repository's own gate.

## What the command cannot see

The command says only what it read.
`docs/project-runtime.md` lists what is outside it under "What the check cannot see".
The two that matter most to a report:

- It reads the default branch as the checkout last fetched it and does not say whether the remote has moved.
  A spawn fetches first, so a goblin can get files the command did not read.
- Its production line reads env files only.
  A spawn also puts the stored credentials of a task's services into the task's terminal, and a test run inherits those with no env file at all.
  Read `auth.json` for the services a task would carry and name them in the report.

## Reading the severities

| Severity | What it means | What to do |
| --- | --- | --- |
| `critical` | It can spend money or reach production. | Raise it before the next dispatch into the project. |
| `high` | A command fails as written, or the project is unsteered. | Fix it in this round of fixes. |
| `medium` | It misleads an agent or wastes a run. | Fix it with the next change to that file. |
| `low` | It is untidy and harms nothing as it stands. | Note it. |
| `ok` | It was proved, and the line says with what. | Keep the lines that count what was examined. |

## Receipt

Report in plain language:

- the verdict line, and how many findings of each severity
- the critical and high findings, one sentence each, with the fix and who places it
- which commands were proved by running, which by a dry form, and which were not run and why
- where the report and the draft are
- what only the Supreme Overlord can supply
