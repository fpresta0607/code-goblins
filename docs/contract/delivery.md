## Delivery

The goblin's branch is its deliverable.

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
Before initial compute, every CI retry, rebuild, split and merge, the train rechecks the selected riders against the actual base and their current pull requests, including after cancelled CI.
A selected head already on that base is recorded and reported as landed before any withdrawal is classified, whoever merged it.
Otherwise a rider that closed, became a draft, gained `hold`, moved heads, changed repository or base, conflicts or waits on review is left off without being retried or blamed, and only the eligible remainder is tested again.
A rider's checks must still pass unless the train retains that rider's recorded own-run proof, whose changed checks follow the hold rules below.
Unknown mergeability holds the step; a waiting rider is rechecked when selected for compute, so its change never invalidates a distinct green half.
Nor does it merge while the branch deploys: a push to the branch starts its deploy workflows, those whose names say deploy, and while a push run of one has not finished, or the newest run of one ended otherwise than successfully, the train waits, says what it waits on in its note and, once for each run and how it stands, in a `merge_train:` message, and merges once that deploy has finished successfully.
A merge during a deploy would start a second one before the first is verified, so a failed deploy could no longer be pinned to one change, and the second could overlap or cancel the first.
A failed or cancelled deploy holds the train until a run of it, run again or started by a fix landed by hand, passes; a skipped run deployed nothing and holds nothing; and the train still tests while the branch deploys, so only its merge waits.
Then the train merges its own pull request with a merge commit, `Merge train <id>: #1, #2`, pinned to the head CI tested: the branch takes that commit, so its tree must then equal the train's tree, and GitHub marks each pull request that rode merged, since the branch then holds its head, with its own number in the branch's history.
A train of one pull request can stand on that pull request's own CI run instead, in a project whose manifest sets `"trains": {"land_lone_rider_on_its_run": true}`: in one week of October 2026, 44 of a private repository's 99 trains carried one pull request and tested again the very tree its own run had just passed.
It does so only when every check on the head is a GitHub Actions check that passed, and every workflow run behind them checked out the pull request's merge commit as GitHub has it now, read from the checkout's own fetch line in the run's log, whose first parent is the branch's tip and whose second is the head that rides.
With that proof it pushes nothing, opens no pull request of its own, and merges the rider's pull request, pinned to its head, after the same rechecks, and the branch must then hold the tree that run tested.
If that proof cannot be established when the train is built, it gets a train run instead.
It holds only where a pull request's own run starts every job a train's run would, so code-goblins, whose own runs test only what a change reaches, leaves it off.
That one merge lands every pull request of the run or none.
A green run blocked by repository policy or an exhausted bounded merge budget stays active with its exact tested head and base, pull request, successful check identities, commits, refusal reasons, failures and spent attempts preserved across polls and restarts.
It leaves its pull request open and its unchanged riders on that train, without pushing another branch, opening a replacement pull request or launching identical CI.
Each merge attempt is spent before the command, even if its acknowledgement is lost; holding and successful polls never reset spent merge attempts or the held error budget.
Unknown readiness holds; after a merge hold, a distinct transition to GitHub's positive readiness (`CLEAN` or `HAS_HOOKS`) permits one normal merge attempt pinned to the head, only after fresh exact head, base, rider and check validation.
Changed successful check identities, resumed or red checks after green proof, or a changed own-run merge identity hold that proof rather than retrying its CI; actual rider withdrawal still removes the rider, and a genuinely changed base requires validation of the eligible remainder there.
These rules apply equally to a train run and a rider's own run, whose successful checks are saved with its initial proof; deploy clearance also preserves that proof.
Both the supervisor and `cfo pr train` publish one durable `merge_train:` hold notice through the existing `pr` wake keyed `train:<owner>/<repo>`, bound to the train and run identity.
Failed delivery is retried without another merge attempt, and a lost publication acknowledgement cannot duplicate the notice.
A normal operator merge of the preserved pull request is reconciled from the actual base without another merge attempt or CI run, and an interrupted landing remains recoverable even after the error budget is spent.
The train then reads that GitHub shows each one merged, up to three times, five seconds apart, and names to you any it still shows open: one whose goblin pushed to it while the train landed keeps what was pushed after and rides the next train once its checks pass on that head.
When the train is over it deletes only its own `cfo/train-*` branch, AFK mode or not, subject to the branch identity check below; it never closes or pushes to a goblin's pull request.
When it is red, the train first runs the failed jobs of that run again, once, with `gh run rerun <run> --failed`, because a check can fail by chance: only a check that is red a second time is a failure the train acts on.
A test that failed by chance has had its second try inside its job by then ([What CI runs](delivery.md#what-ci-runs)), so what the train's second try answers is a job that failed outside its tests, such as a runner that died or an install that failed, or a test that failed twice in a row.
A run that is green on its second try lands as a green run does, and the train's record and its `merge_train:` message name each check that failed once and passed when it ran again, with its link, so a test that fails by chance is counted and never hidden.
A test that failed once inside a check that still ended green is counted the same way: as a run passes, the train reads the run's Failed once warnings in one call, and its record and its `merge_train:` message name each such test, or say that they could not be read, which never holds a landing back.
No run is tried a third time: whether a run was tried again is read from GitHub's own count of its attempts, so a restart in the middle of asking never asks twice, and a failure stands only once the workflow run itself ended red on its second attempt, since a pull request's checks can show the first try for a while after.
A red check that no workflow run stands behind, such as another service's, cannot be run again and is acted on as it stands.
When the run is red a second time, the train is halved: the first half rides alone, every half that passes lands, and the halving goes on until one pull request fails alone, each of those runs with a second try of its own.
That pull request's goblin gets the failing checks with their links, in its own terminal, and the pull requests not yet tested wait for the next train.
No pull request is blamed while the branch's own push CI is red at the commit the run was built on: the train stops and says to fix the branch first.
A run whose base moved before it landed is built again on the new tip and tested again after reconciling heads already on that base and rechecking the remaining riders; after three such runs in a row the train stops and asks you to hold merges.
This also applies after a partial landing: only the eligible remaining source is validated on the changed base.
Every step is recorded before it acts, so a restart or a network failure in the middle of one, even halfway through landing, is taken on by the next step, and a merge that went through although its answer was lost is found on the branch.
Before publishing, rebuilding or finishing, the train checks the remote branch against its recorded heads; branch writes and deletion require an exact conditional lease, so an externally added commit is never overwritten or deleted.
An unknown or changed branch identity holds for reconciliation, including during restart and error cleanup.
A successful push whose acknowledgement was lost resumes publication of the recorded head without rebuilding or pushing it again, and an unrecorded open train pull request is recovered by its branch.
Before green proof, transient command and network failures retain their bounded handling: a failed step is retried on the next poll, reaches you after three consecutive failed polls and stops after five, unless landing recovery or branch identity requires a hold.
A run without green proof whose checks never appear stops after 20 minutes and one whose checks never finish after three hours.
A new train pull request receives its CI window immediately before creation, including after publication recovery; recovering an already-created pull request never renews its saved deadline.
A start that is cut short, as by one read that ran out of time, is kept and counts as the train's first failed step, so the next poll resumes its recorded build or publication rather than starting a replacement train.
Each outcome reaches you as a `pr` wake keyed `train:<owner>/<repo>`, starting with `merge_train:`, and names any goblin it could not tell, for you to relay; while AFK mode is on, each pull request a train merges is logged as merged with the run that proved it, and one whose selected head the train finds already on its base is logged as already there, once in the stretch.
The supervisor writes each of those lines, from the train's own record and the state GitHub gives the pull request, whoever started the train: a `cfo pr train` started detached does not run under the registered CFO, which the log of a decision needs, so it asks the supervisor to log the landing instead of writing it.

On GitHub a train that landed leaves nothing closed without merging, which GitHub draws in red: its pull request reads merged, and so does every pull request it landed.
A finished train that did not land its last run closes that run's pull request with how it ended, the failed checks and what landed before it: the one place on GitHub a train that went red says so.
Halving adds no second one, because each half that passes merges a pull request of its own and the half after it opens a new one, and a train that stopped before it pushed anything opened none.
Until 2026-10-10 a train merged each pull request by itself and closed its own without merging, so every train that had done its job read as a failed pull request.
A train an older build started uses the same landing checks: its pull request is retitled and labelled before it merges, and a landing that build left half done follows the partial-landing validation above.

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
