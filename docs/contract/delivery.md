## Delivery

The goblin's branch is its deliverable.

**Delivery intake.**

Resolve project posture and concrete change risk before dispatch, independently of harness, review budget and merge authority.
The project's `project.json` may declare `delivery.posture`: `no-mistakes`, `no-mistakes-prod-only`, `direct-PR`, or `local-only`.
The brief's Delivery section records `surface: internal|product|mixed|unknown`, `risk: low` or the concrete sensitive surface, and a change-specific `reason:`.
Internal means tooling, automation or contributor and operator process; a directory or project name does not establish that classification.
The conditional posture chooses direct-PR only for known internal, low-risk work with defined project checks; product work uses no-mistakes.
Mixed, unknown or malformed assessments, missing or unreadable policy, credentials, authorization, state recovery, migrations and high-risk publication use no-mistakes.
An explicitly configured direct-PR or local-only posture is standing Overlord authority for assessed low-risk internal or product work with a concrete reason and defined checks.
It does not narrow the strict boundaries for sensitive, mixed, unknown or malformed work.
Only a concrete separate Overlord exception may narrow the selection beyond that standing posture, recorded with `--delivery-exception` and an explicit mode at brief and spawn.
An explicit no-mistakes mode can always increase rigor.

`cfo brief` and `cfo spawn` use the same resolver.
Before the worker starts, spawn saves `delivery.json` in its task directory with the mode, posture, surface, risk, reason, project checks and CI requirement.
The task record binds its bytes by `delivery_hash` and keeps its mode separately.
Verification, pipeline entry and PR registration refuse a changed receipt or a mismatching mode.
`cfo verify` uses the frozen verification tiers, time limits and declared reuse inputs, so later project-policy edits cannot silently change a task's proof.
A helper remains local-only under its parent's publication owner.
Existing tasks without this receipt retain their modes, frozen policies, findings, spent rounds and custody.
Intake never migrates, restarts or repairs a running validation.

**One validation owner.**

The selected path owns its rigor.
Simple work runs its defined project checks and required CI through the existing direct-PR or local-only route.
Do not attach an independent model review, serial manual clean-verdict gate or second unchanged baseline.
If the change needs independent validation, select no-mistakes instead of inventing another gate.
Under no-mistakes, the same implementation worker drives `cfo pipeline run` and `respond` through every synchronous return.
The pipeline alone owns review, repair, tests, documentation and publication; any focused independent question belongs within that contract.
The CFO still inspects the concrete change and relevant proof before delivery.

Once validation starts, new product requirements normally become follow-up work.
Corrections needed for accepted behavior, equivalent behavioral test replacements and accurate documentation remain in scope, including their smallest downstream changes.
The worker never hand-edits, commits, pushes or starts another run while the active pipeline owns the branch.
An explicit instruction invalidating the entire change uses the existing supported custody procedure under the CFO's direction, preserving history and spent allowances.
This contract adds no native recovery procedure or authority over another task's run.

**Finding authority.**

The implementation worker never decides its own ask-user finding.
It reports the actual finding to the CFO and applies only the returned decision through the active gate.
The CFO reconstructs accepted intent, identifies what Fix would require, then names the decision's run, step, action, finding IDs, instructions and authority.
Reviewer labels cannot amend the accepted contract.
Routine corrections, restoring approved behavior, completing the approved design, equivalent tests and correcting final-diff delivery evidence are autonomous CFO decisions within that contract.
Difficulty or an unforeseen file is not a new product decision.
Escalate unresolved product choices, a new guarantee or subsystem, a broader threat model or maintenance obligation, destructive or security-sensitive actions, and requests to waive unresolved defects.
State the accepted requirement, proposed expansion, smallest compliant alternative, consequences and recommendation.
When repeated findings preserve a questionable abstraction, reconsider it before another round.
Never use `--yes`, skip required proof, approve an exhausted budget or open a fresh run to escape it.

**Proof and final candidate.**

Formatting and documentation belong in the candidate before final proof.
Later changes invalidate the proof they affect; the selected path validates the final candidate.
Publish only the commit the proof names, with every required check completed successfully on that head.
Local same-user records cannot replace a required hosted check.
Reuse a pass only through existing receipt checks matching source, commands and configuration, dependencies and ignored inputs, tools, environment and platform.
Changed relevant inputs invalidate that pass; do not add a generic evidence cache or duplicate baseline.
Fast Go verification and prepush both execute required policy guards even when their package is slow, without a separate guard run when the selected package tests already include it.
Name platform-specific guards deferred to CI as deferred, never as executed on another platform.

**Completion and scouts.**

A ready result names the public PR URL and exact published head with required CI green, or a clean committed branch for local-only work.
A draft, a different local commit or a commit present only in a disposable validation worktree is not ready for landing.
Use existing PR check and guarded merge commands; confirm the forge's landed state before cleanup and never discard unlanded work.
Delivery mode and merge authority remain independent.
A scout leaves a self-contained report, and a recommendation or visual review grants no implementation authority.
Keep a review artifact available while feedback is outstanding.
When implementation is separately authorized, inventory scratch changes and dispatch the ship through `cfo brief` and `cfo spawn` from the proper clean base, carrying only intended implementation and regression changes and retaining the scout's report as context.
There is no dedicated scout-promotion command in this build; a report or a mode edit never supplies missing ship authority.

- `no-mistakes` - the goblin uses `cfo pipeline run`/`respond` under its frozen policy, so an exhausted review budget or fail-closed start refusal comes back as unresolved work needing your decision rather than an automatic approval; follow [docs/pipeline.md](../pipeline.md), and after a completed gate relay the PR URL and wait for merge authority, then `cfo pr merge`.
- `direct-PR` - open the PR as a draft (`gh pr create --draft`), record it with `cfo pr check`, mark it ready (`gh pr ready <url>`) once, on the head it means to merge, report it done once that run is green, and wait for merge authority before `cfo pr merge`.

A pull request is a draft until its head is the one its goblin means to merge, so the fix rounds before that start no hosted CI: a repository's workflows skip a draft and run on `ready_for_review`, and each push to a ready pull request runs them again.
A run whose event saw a draft first asks GitHub whether the pull request is a draft now, in one short job, and runs every other job unless it still is: an event can see the pull request as it was a moment before, and on 2026-10-15 a push seconds after a pull request was marked ready saw a draft, skipped every job and cancelled the ready run, so no run tested its last head.
That job's check, `draft state`, tests nothing: on a draft it succeeds while every other job is skipped, so `cfo pr merge`, a merge train, the CI wakes and a card's hosted checks pass over it, whatever it concluded, as they pass over a skipped check.
The goblin's first instruction says so for a direct-PR task.
It tells a direct-PR or gated goblin too that a later round is pushed once, whole, and only once its pull request's CI has ended: a push while that CI runs cancels the run after it billed its minutes, as 18 runs of one private repository did in one week of October 2026.
A gate opens its pull request ready, at the head its review, tests and lint passed, since its `ci` step waits on that pull request's CI: opened as a draft, a workflow that skips drafts would leave the step nothing to wait on, or skipped checks it would read as passed.
A merge train and `cfo pr merge` take only a ready pull request, and only one on whose head a check ran: a check that was skipped or neutral tested nothing, though GitHub counts it as passing, and a pull request just marked ready shows its draft run's skipped checks until its ready run registers.
A deploy status the host refused for its rate limit, a failed Vercel status whose target is its upgrade page with `upgradeToPro=build-rate-limit`, counts as not run in the same way, never as failed: the host never built the head, and pushing again for a fresh status spends another build of the same allowance.
The train, `cfo pr merge`, the CI wakes and a card's hosted checks name it as rate limited, and any other failed deploy status still fails.
gh's check rollup gives a commit status's state and target but never its description, so the target is what is read.
The supervisor raises no `ci_finished` for a draft, and no `pr_health` for a draft that is only behind its base.
- `local-only` - the goblin stops with a clean branch; land it with `cfo merge-local <id>` only on the Supreme Overlord's word.

`cfo pr merge` and `cfo merge-local` never merge red or divergent work - they refuse loudly.
After a merge, tell the Supreme Overlord the full PR URL.
Where the base requires GitHub's merge queue, `cfo pr merge` adds a green PR to the queue rather than merging it: the queue tests the PRs in it together on the base's current tip and merges each whose run passes, so a PR no longer needs its head refreshed on main and a fresh CI run before its turn.
It is merged when GitHub says so, not when the command returns.
While [AFK mode](afk.md#afk-mode) is on, merge authority is your own merge word for a goblin pull request that meets its checks, given with `cfo pr merge <url> --verified "<what verified it>"`.

### What CI runs

A pull request's checks come from `.github/workflows/go.yml`, whose jobs run at once: the frontend's checks, the board's browser tests in four jobs, each slow Go package in two jobs and cmd/cfo in three, and every other package in one.
A merge train's run and a run on the default branch start every one of them, so nothing reaches the default branch that the full set did not test.
A push to the default branch starts none of them again when a pull request run of the same workflow that started every job passed on the push's exact tree and first parent, which is what a merge train's run is for the tree the train lands: that run leaves a record named for the pair, kept a week, and the push's plan job finds it with `tools/pushreuse`, takes it only from a green pull request run whose head is in this repository, and names it in the push's `test` check.
Any other push, one whose records cannot be read, and one whose pull request was tested on an older main start every job as before, and `install.yml` reuses its own records the same way.
From 2026-09-14 to 2026-10-14, 482 of 623 pushes to main ran a tree their pull request had just passed on the same main.
A pull request's own run starts the jobs its change can alter, which a `plan` job chooses first (`tools/ciplan`): a Go job when the change reaches one of its packages, read as `cfo gate test --plan` reads it under `config/verify.json`, and the `frontend` and browser jobs when the board's source changed or a file they read outside it.
It starts every job when the change touches a workflow, `config/verify.json`, the plan's own code, `go.mod` or `go.sum`, or a file the policy does not account for, since what the rule cannot be sure of widens the run.
`JOBS`, at the top of the workflow, lists each job with what can change its result, and the plan's log says for each job whether it runs and why.
So a pull request that changes only a document nothing embeds starts no test job, one that changes only the board starts the frontend and browser jobs, and one that changes Go starts the Go jobs its change reaches.
The one check branch protection requires, `test`, passes only when the plan and every job it started passed: a job the plan left out shows as skipped, and a skipped job is a pass only then and only in a pull request's own run.
A test that fails runs once more in its own job: a Go test by its name once the job's other tests have ended, and a browser test at once in a fresh browser.
One that passes on its second try leaves the run green and is named, with its job and its package or spec file, as a warning titled Failed once on the `test` check and in the run's summary, so a failure by chance neither fails a pull request nor passes unseen: `gh run view <run>` prints each one.
One that fails twice fails its job, and so does a failure no test names in full, such as a package that does not compile or a panic or timeout that cut the tests short, since running the named test again would pass over the tests it never reached.
A run takes as long as its slowest job, about 8 minutes on GitHub's runners as measured on 2026-10-10, where it took 20 while the browser tests ran as one job.
A pull request's own run waits for the plan first and is then as long as the slowest job it starts, so it is shorter when its change reaches few jobs.
A pull request reaches the default branch after its own run and then its train's, which is under 20 minutes from its push when nothing waits for a runner.
Runs of one account share its runners: when eight runs started together on 2026-10-10, each job waited 13 to 15 minutes for a runner, so a burst of pushes or merges costs more than the sum of its runs.

### The board's render budget

Every change to the board keeps the Overlord's rule of 2026-10-27, that it loads intelligently: panels, terminals and long histories load on demand and are windowed, an update redraws only what it changed, an animation runs only while something happens and stops off screen and under reduced motion, and nothing polls when an event can say it.
`frontend/tests/render-budget.spec.ts` holds the board to it in CI: an idle board commits nothing between its minutes, a snapshot that changes nothing or only the meters redraws no card, one that changes one goblin redraws that card alone, and a board with no goblin at work asks for no animation frame.
A change that needs more widens that budget in the same pull request and says why, and the goblin's done report shows the render counts or a profile of its change.

### Merge trains

Landing pull requests one at a time costs one CI run each, in a row, because every merge makes the other green runs stale.
A merge train lands them together with one run.
`cfo pr train <project>` picks the green pull requests goblins finished on the project's default branch in queue order, the one reported done first going first, and merges them onto the branch's tip with merge commits on a branch of its own, `cfo/train-<stamp>`.
A pull request a goblin reported done since its task was spawned is finished whatever the goblin reports after, such as working on its next pull request or being blocked, so it keeps its place and rides while it is open, green and not in conflict; a head it pushes after the train took the pull request leaves it off that train.
A report outlasts the goblin's relaunch, which a pause's resume, a switch and a comeback after a restart each are, but then it stands only for the head it was made of: the pull request rides while every check of its head had finished by the time of the report, so it was green then and is unchanged since.
A check that finished after such a report means a new head or a new run, and the train leaves the pull request off, saying it rides once reported done again.
A task's record keeps the generation it was spawned under as `first_gen`, beside the `spawn_gen` every relaunch renews, and a done report is read from there on.
That is what the cut guards: a cleaned-up task's status log is kept, a task respawned under the same id appends to it, and the earlier task's done reports are never the new goblin's.
A task an older build spawned records no `first_gen` until its first relaunch, which writes the generation it ran on until then, so its reports are read from its latest relaunch as before, and a pull request it reported done before that rides once reported done again.
It builds them from commits alone, so the project checkout's working tree and branches are never touched, each with the merge commit GitHub's own merge of it would leave, `Merge pull request #N from owner/branch`, and pushes the result with a pull request of its own, titled `chore(cfo): merge train for PRs #1, #2`: CI tests the combination once.
A pull request that does not merge cleanly with the ones ahead of it is left off, and once the train is over its goblin is told to merge main, once per head.
When the run is green, the train first reads every rider again: one closed, turned into a draft, labelled `hold` or moved to another head since it rode is left off and the rest is tested again, so the branch never receives part of what CI tested.
Nor does it merge while the branch deploys: a push to the branch starts its deploy workflows, those whose names say deploy, and while a push run of one has not finished, or the newest run of one ended otherwise than successfully, the train waits, says what it waits on in its note and, once for each run and how it stands, in a `merge_train:` message, and merges once that deploy has finished successfully.
A merge during a deploy would start a second one before the first is verified, so a failed deploy could no longer be pinned to one change, and the second could overlap or cancel the first.
A failed or cancelled deploy holds the train until a run of it, run again or started by a fix landed by hand, passes; a skipped run deployed nothing and holds nothing; and the train still tests while the branch deploys, so only its merge waits.
Then the train merges its own pull request with a merge commit, `Merge train <id>: #1, #2`, pinned to the head CI tested: the branch takes that commit, so its tree must then equal the train's tree, and GitHub marks each pull request that rode merged, since the branch then holds its head, with its own number in the branch's history.
A train of one pull request can stand on that pull request's own CI run instead, in a project whose manifest sets `"trains": {"land_lone_rider_on_its_run": true}`: in one week of October 2026, 44 of a private repository's 99 trains carried one pull request and tested again the very tree its own run had just passed.
It does so only when every check on the head is a GitHub Actions check that passed, and every workflow run behind them checked out the pull request's merge commit as GitHub has it now, read from the checkout's own fetch line in the run's log, whose first parent is the branch's tip and whose second is the head that rides.
Then it pushes nothing, opens no pull request of its own, and merges the rider's pull request, pinned to its head, after the same rechecks, and the branch must then hold the tree that run tested; a base that moved first, a merge GitHub made again since, or a check no workflow run stands behind gets a train run as before.
It holds only where a pull request's own run starts every job a train's run would, so code-goblins, whose own runs test only what a change reaches, leaves it off.
That one merge lands every pull request of the run or none: a merge GitHub refuses landed nothing, and the next step reads the branch and the riders again before it tries.
The train then reads that GitHub shows each one merged, up to three times, five seconds apart, and names to you any it still shows open: one whose goblin pushed to it while the train landed keeps what was pushed after and rides the next train once its checks pass on that head.
When the train is over it deletes its branch, its own `cfo/train-*` branch and never a goblin's, AFK mode or not, and never merges, closes or pushes to a goblin's pull request.
When it is red, the train first runs the failed jobs of that run again, once, with `gh run rerun <run> --failed`, because a check can fail by chance: only a check that is red a second time is a failure the train acts on.
A test that failed by chance has had its second try inside its job by then ([What CI runs](delivery.md#what-ci-runs)), so what the train's second try answers is a job that failed outside its tests, such as a runner that died or an install that failed, or a test that failed twice in a row.
A run that is green on its second try lands as a green run does, and the train's record and its `merge_train:` message name each check that failed once and passed when it ran again, with its link, so a test that fails by chance is counted and never hidden.
A test that failed once inside a check that still ended green is counted the same way: as a run passes, the train reads the run's Failed once warnings in one call, and its record and its `merge_train:` message name each such test, or say that they could not be read, which never holds a landing back.
No run is tried a third time: whether a run was tried again is read from GitHub's own count of its attempts, so a restart in the middle of asking never asks twice, and a failure stands only once the workflow run itself ended red on its second attempt, since a pull request's checks can show the first try for a while after.
A red check that no workflow run stands behind, such as another service's, cannot be run again and is acted on as it stands.
When the run is red a second time, the train is halved: the first half rides alone, every half that passes lands, and the halving goes on until one pull request fails alone, each of those runs with a second try of its own.
That pull request's goblin gets the failing checks with their links, in its own terminal, and the pull requests not yet tested wait for the next train.
No pull request is blamed while the branch's own push CI is red at the commit the run was built on: the train stops and says to fix the branch first.
A run whose base moved before it landed is built again on the new tip and tested again, since what it proved is not what would land; after three such runs in a row the train stops and asks you to hold merges.
Every step is recorded before it acts, so a restart or a network failure in the middle of one, even halfway through landing, is taken on by the next step, and a merge that went through although its answer was lost is found on the branch.
A step that fails counts against the train, which takes it again on the next poll, tells you of it once it failed on three polls in a row and stops after five; a run whose checks never appear stops after 20 minutes and one whose checks never finish after three hours.
A start that is cut short, as by one read that ran out of time, is kept and counts as the train's first failed step, so the next poll builds the train and nobody is woken for it.
Each outcome reaches you as a `pr` wake keyed `train:<owner>/<repo>`, starting with `merge_train:`, and names any goblin it could not tell, for you to relay; while AFK mode is on, each pull request a train merges is logged as merged with the run that proved it, and one the train finds already on its base, landed by an earlier run or train, is logged as merged saying so, once in the stretch.
The supervisor writes each of those lines, from the train's own record and the state GitHub gives the pull request, whoever started the train: a `cfo pr train` started detached does not run under the registered CFO, which the log of a decision needs, so it asks the supervisor to log the landing instead of writing it.

On GitHub a train that landed leaves nothing closed without merging, which GitHub draws in red: its pull request reads merged, and so does every pull request it landed.
A train that did not land its last run closes that run's pull request with how it ended, the failed checks and what landed before it: the one place on GitHub a train that went red says so.
Halving adds no second one, because each half that passes merges a pull request of its own and the half after it opens a new one, and a train that stopped before it pushed anything opened none.
Until 2026-10-10 a train merged each pull request by itself and closed its own without merging, so every train that had done its job read as a failed pull request.
A train an older build started lands the new way on its next step: its pull request is retitled and labelled before it merges, and a landing that build left half done is finished by merging the train's pull request, which leaves the branch at the tree CI tested.

A train's pull request wears the `merge-train` label from the time it opens, and the train makes sure of the label again before it merges one, so a pull request an older build opened, or one whose label was taken off, merges wearing it too.
The notes GitHub writes for a release list every merged pull request, and this repository's `.github/release.yml` leaves that label out, so a release's list of what changed, and the board's Update item that reads it, name each pull request a train landed and never the train.
A repository's first train makes the label, and a label the repository already has is left as its owner keeps it.
The label never stops a train: one that could not be put on reaches you as a `merge_train:` wake that names the pull request and the command that puts it on by hand.
Another repository that asks GitHub for a release's notes leaves the label out in a `.github/release.yml` of its own.

The supervisor runs trains by itself: on its GitHub poll, when two or more green pull requests goblins finished wait on one default branch, or one has waited alone 10 minutes since its goblin first reported it done, it starts a train, and it takes each running train a step on every poll, also once the goblins of its repository have left.
A project's manifest can make one alone wait longer for a second to share a train run with, `"trains": {"lone_rider_wait_minutes": 30}`, up to 240; one that can land on its own CI run still rides after 10 minutes, since that costs no run and a second would save nothing.
A train that ends on a poll's step reads the open pull requests again and starts the next train on that same poll, so a pull request reported done while a train ran rides as soon as that train is over.
`cfo pr train` joins the train already running in the project and waits until it is over.
Each train records every CI run it starts, with the pull requests it tested and how it ended.
The board shows one card for each batch of pull requests: a train that landed nothing is folded into the later train that took its pull requests on, which counts its runs on from it, and one that landed nothing and was not retried shows once, saying why.
A running train heads In progress, and a finished one heads Completed for six hours.
A finished card lists every pull request on its train, and one it could not merge or left for the next train says there what happens next.
A click on a train's card opens its panel: every pull request of the batch with its goblin, and every run with the pull requests it tested and how it ended, each one tap from GitHub.
While a train runs, hold other merges to its branch: a merge outside it makes the train test again.
PR health wakes leave alone a train's own pull request and the pull requests a running train carries.

A pull request rides when it is into the default branch from this repository, opened by the account `gh` works as (a teammate's never rides, even one a goblin reported), its goblin's latest reports are `done` naming it, every check on its own head passed and at least one of them ran rather than being skipped, it is not a draft, wants no review, is not in conflict with the base, and carries no `hold` label.
Label a pull request `hold` to keep it off every train: one only the Supreme Overlord's word merges (recovery, security, money paths) or one he said to wait on (`gh pr edit <n> --add-label hold`, after `gh label create hold` once per repository).
A pull request a train found broken stays off trains until its head changes.

**Firstmate attribution.**

The intake, validation ownership, finding authority and completion contracts adapt the delivery sections of [Firstmate at bb3e690](https://github.com/kunchenguid/firstmate/tree/bb3e6906548b5c03d3212e9f060bafde982e0cf1) and its validation-supervision, ask-user-authority, ship-landing and scout-completion skills.
The upstream notice is retained here for the adapted material.

MIT License

Copyright (c) 2026 Kun Chen

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
