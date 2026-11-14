## Dispatching

`goblins spawn` is the only way to start goblin work.
It validates the id and mode before touching anything, refuses before starting anything while free memory or free disk is under its floor, naming what is free and the floor, acquires a fresh git worktree of the project at `$GOBLINS_HOME\worktrees\<project folder>\<id>` (never the primary checkout, which gains no folder and no file), gives it a scratch folder at `$GOBLINS_HOME\scratch\<id>` that its `TEMP`, `TMPDIR` and `GOTMPDIR` point to, with its `TMP` pointing to `$GOBLINS_HOME\scratch\.tmp`, the one folder every goblin shares, provisions it per the project's worktree manifest (its own read-only copy of each config file the manifest names and of no other, linked dependencies, the token-authenticated subset of the project's `.mcp.json`), starts the goblin's own native terminal, named by its task id, prepares its environment (worktree location plus harness environment), starts the harness and delivers its brief instruction, and reports `spawned ...` only after confirming the agent is working.
A project whose dependencies are installed ([strategy `install`](worktrees.md#project-worktree-environment)) gets no install from the spawn: the brief instruction tells the goblin to run the install commands in its worktree as its first step, the `spawned` output names them on a `dependencies:` line, and the goblin's card shows `installing its dependencies first` until its own first report.
Those commands only ever install a lock with hashes: a project whose manifest names an install that resolves versions at a package index gets no install step, a dependency list with no lock of its own gets no install of it, the `dependencies:` line says why, and the goblin is told to ask for a lock in a blocked report rather than resolve the versions itself.
Starts take turns on the home's spawn lock only for what must be serial: the id's alias check, the admission that counts running terminals, the worktree and branch, the task record and the terminal's launch.
The credential probes run before the turn, and the harness's startup, the brief's delivery and the goblin's install after it, so a slow start holds up no other start or resume; Resume and the comeback after a restart take the same turn around their terminal's launch only, and are admitted again under it.
A start that finds another start's turn waits for it, says on standard error which start it waits on and since when, and stops after 10 minutes naming that start, which is then stuck rather than slow.
`TMP` is the one temporary variable that is not the task's own, because of how Git Bash decides `/tmp`: its first shell to start while no other runs asks Windows for the temporary folder, which is `TMP`, and that folder is `/tmp` for every Git Bash shell of the user, whatever `TMP` a later one has, until the last of them ends.
A goblin whose `TMP` named its scratch folder made that folder the machine's `/tmp` whenever its shell came first, and cleaning the task up then took `/tmp` from every Git Bash on the machine.
So what asks Windows for the temporary folder in a goblin's terminal, as a Go program's `os.TempDir` and a .NET program do, lands in the shared folder.
Everything else lands in the task's own scratch folder and goes with the task: its build output, its logs, what its shell tools and scripts put in `TEMP` or `TMPDIR`, and the folders a Go test's `t.TempDir()` makes, which follow `GOTMPDIR`.
A goblin started before this keeps the `TMP` it was started with until it is resumed or switched.

- `--brief` must be an absolute path to an existing file that satisfies the [brief readiness contract](../commands/brief.md), or for a helper's spawn the [helper contract](#helper-goblins).
- The brief's Authentication section decides what the goblin can reach under [Credentials by need](authentication.md#credentials-by-need).
- The brief's Verification section assigns project checks, tier execution and reuse to the [selected validation owner](delivery.md#delivery).
- `--mode` is `no-mistakes`, `direct-PR`, or `local-only`; omitted, [delivery intake](delivery.md#delivery) resolves project posture and concrete change risk, with no-mistakes for missing or uncertain inputs.
  Given, it is the mode the brief's Delivery section names, and a spawn that asks for another is refused.
- Every goblin starts in a native terminal of its own, except a Claude Code goblin its home puts on stream-json pipes ([docs/native-board.md](../native-board.md)).
  A pi goblin starts with `--approve` where its pi advertises it, trusting the folder's project files for that run only, so it never asks.
- Every goblin's terminal names a browser session of its own, `CHROME_DEVTOOLS_AXI_SESSION` set to its task id, and the CEO's is `goblins`. chrome-devtools-axi keeps one bridge and one browser for each session name, so a bridge belongs to one owner and is never the tool's unnamed session, which every program of the user shares.
  The bridge carries its terminal's mark and ends with its goblin at a pause, a stop or the janitor's sweep.
  On a supervisor with a board, `CHROME_DEVTOOLS_AXI_BROWSER_URL` points the bridge at the goblin's own browser, which the supervisor runs so the board can show it.
- Every goblin's first instruction, a helper's and a takeover's too, says that what it starts is its own to end: browsers headless and programs hidden in tests unless a picture needs a window, every start tracked by its pid and ended by that pid, never by its name, and no retry or polling loop without a cap.
  `goblins brief` and the board's Start write the same into a brief's Constraints, and the supervisor tells you what it sees of it ([Supervision](supervision.md#supervision)).
- `--class` is `ordinary` (default), `high-risk`, or `mechanical`; it freezes the task's review repair budget at spawn - two cycles for ordinary and mechanical, three for high-risk - and a running task's snapshot never changes ([docs/pipeline.md](../pipeline.md)).
- `--yolo` lets you decide routine gates inside the Supreme Overlord's request; without it, every merge asks the Supreme Overlord.
- The Supreme Overlord can start a queued task himself with **Start** on the board, which runs this same `goblins spawn` with the task's brief, only the harness, model and effort its backlog row or the brief's Delivery section explicitly names, with the row taking precedence for each of the three, and the mode that section names (see docs/native-board.md).
  Unspecified engine fields stay empty for the lane table to choose, both on the queued card and in a brief written from its row, and automatic starts use the same rule.
  A queued field saved as Automatic writes an explicit empty row override, keeping the brief and the other preferences intact.
  Start is refused under 5 GB of free memory or of free commit (RAM plus page file), while its row waits for something that has not cleared, or for good once the task already finished, which its card says.
  A task with no brief gets one written from its queued row first, with a `brief created: ...` notify so you can amend it.
  A row carries no change assessment, so its task starts strict, and that brief's Verification section gives the checks to the pipeline whatever mode the row names.
  It puts the task at the top of In progress and wakes you with a notify: `started: the Overlord started this from the board, ...` means it is dispatched, so update its backlog row as after your own dispatch.
  A `start failed: ...` names why `goblins spawn` refused.

### Heavy local runs

One heavy run goes at a time on this machine, whoever started it.
On 2026-10-10 the same 423 tests took 314 seconds in one gate run and 1,064 in another because several runs tested at once, and over 16 days a test agent beside other gate work was two to three times as likely to be cut at its time limit, with no assertion failing.
So a whole test suite, a full build, a browser suite or any other heavy local run goes behind the machine's one line: `goblins gate turn -- <command>`.
`goblins gate test` and `goblins gate prepush` stand in that line by themselves, and so does each command `goblins verify <id>` and `goblins security <id>` run, so none needs a wrapper, and a test run by name needs no turn.
Every goblin's first instruction says so, and so does the one a goblin taking over a task gets, so a brief need not repeat it.
A gate's wait is never a heavy run: a gate's test command takes its own turn, so `goblins gate turn` refuses `no-mistakes axi run`, `respond` and `answer` and `goblins pipeline run` and `respond`, which wait on a gate and would keep every other run in line through the gate's agents' work.

While it waits a goblin sees one line on standard error as the wait starts, whenever its place in line changes and once a minute, then one line when it takes its turn, then the command's own output and its exit code:

```text
goblins gate turn: waiting for its turn (3m0s so far): the turn is held by go test ./... in C:\work\repo, task Zora (cg-example) (pid 4242), for 12m0s; this run is next in line
goblins gate turn: took its turn after 3m0s
```

The wait has no time limit, so a goblin starts the run in the background, where its tool's time limit cannot cut the wait short, with `goblins gate turn --detach <log-file> -- <command>`, and reports a long wait with `goblins notify <id> --waiting-on <the holder's task, or memory> "<why>"`.
The run starts hidden, in a console with no window, writes its pid to `<log-file>.pid` and its output to `<log-file>`, whose last line says how it ended, and the goblin is told when it ends.
A turn held by "the janitor, pruning the <tool> cache" is the hourly sweep's, which takes one only on a free line and gives it back once that prune ends, ten minutes at most, so a goblin waits it out with no report.
A goblin never starts a heavy run with a process start of its own, such as `Win32_Process Create`, `Start-Process` or `start`, and a brief never tells it to.
On 2026-10-14 such starts left blank terminal windows on the Supreme Overlord's desktop for as long as each run waited in the line, and closing one ended its run.
So the product closes the class as well: a `goblins` process that finds itself alone in a console Windows made for it, with a window, leaves that console as it starts, and the console host's window, or the Windows Terminal window it was handed to, closes within a moment.
What he starts to look at keeps its console: the quick start and its `goblins` forms, `goblins attach`, and `goblins` with no command, and any command he runs in a terminal he opened shares that terminal's console, which it never leaves.
The supervisor's start watch is the backstop for any other program a goblin starts that way ([Supervision](supervision.md#supervision)).
Claude Code starts a turn when a background run ends, and Codex and pi do not, so a `goblins gate turn` or `goblins gate prepush` run in a Codex or pi goblin's own terminal and worktree types one `CEO:` line into that goblin as it ends, as `goblins send` would, when the goblin sits idle at its prompt: what ran, its exit code and how long it took.
A goblin in a turn is not told, since one that waited on the run there has read how it ended, and an interrupted run tells nothing.
On 2026-10-13 Codex goblins sat at their prompt 17 to 26 minutes after their own run had ended.
`goblins gate turns` shows the line from anywhere.

The line holds a run back while free memory or free commit is under 4 GiB, and it covers a run that gives its memory back when its command ends.
A brief that told a goblin to ask you for the machine before a whole suite or a heavy build no longer needs to, since standing in the line is the ask.
Two kinds of run still need your word first, and a brief keeps saying so:

- **A WSL or Docker run.** Its virtual machine keeps the memory after the command has ended, where no turn gives it back.
- **A load or stress run.** It is meant to fill the machine, and the line would only hold every other run back behind it.

### Helper goblins

A goblin may ask the supervisor for one helper goblin of its own, as the Supreme Overlord answered on 2026-09-28: "Allow it through the supervisor, with memory caps".
It writes the helper's brief to a file and runs `goblins helper start <its-id> --brief <file> --title "<short title>"` from its own terminal; the request rides the supervisor's pipe, and only a process inside that goblin's terminal can ask in its name.
The supervisor alone starts helpers, through `goblins spawn <id>-h<n> --parent <id>`, and only within the caps: one helper per goblin at a time, a paused helper included; no helpers of helpers; a parent that is live, native, at work and on a branch; and the memory, commit, disk and live-goblin marks every Start holds (5 GB to start, never under the 4 GB floor).
A helper also waits, as everything the supervisor starts by itself does, while memory and commit, less the about 1 GB its session takes, are short of the floor the machine's heavy line keeps for its next test run: the refusal names what is free and that floor and says to ask again in ten minutes, and a line that cannot be read holds no helper and is said on the board.
A refusal says why and when to ask again, and one goblin starts at a time, so a helper waits behind the board's Start or a Resume, and they wait behind it.
The board's Start and Resume are one click: one clicked while another goblin starts or resumes, or while memory or disk is short, waits its turn on the supervisor rather than being refused, and one that fails reaches you as `start failed:` or `resume failed:`.
A message the Overlord types to a paused, resuming or starting goblin is queued and delivered once, and one its goblin cannot take within ten minutes reaches you as `message not delivered:` with his words, yours to relay.
A start that meets a `goblins spawn` you run by hand, which holds the home's spawn lock until its terminal runs, waits for its turn, and one that gives up after waiting 10 minutes tries once more as soon as the lock frees, as Start, Resume and the scheduler's own starts and resumes do.
The helper is a local-only ship task named `<parent>-h<n>`, a number no earlier helper of that parent had, in a worktree of its own on `<parent-branch>-h<n>`, cut from the parent's last commit.
Its brief is kept at `data/<helper>/brief.md`, and its own task determines its routing independently of the parent's harness, model and effort.
Explicit engine settings in the helper's brief override routing, while a mode other than local-only is refused before the start is recorded.
Its project comes from its parent; its Authentication choices follow the [helper credential limits](authentication.md#credentials-by-need).
Its spawn checks the helper contract instead of brief readiness: a nonempty brief without generated TASK, ACCEPTANCE or CONSTRAINTS placeholders whose Authentication section has one explicit `credentials:` and one explicit `mcp:` line, with no Acceptance criteria, Constraints, Delivery mode or `ready: true` required and no `goblins brief ready` to run.
It reports to its parent, not to you: its `goblins notify <helper> --done`, which needs no pull request, its `--blocked` and its `--failed` are typed into the parent's terminal, and wake you only when that terminal cannot take them.
You are told once when each helper starts or fails to start, and `goblins fleet-view` names each helper's parent.
The parent answers its helper with `goblins send <helper> "<answer>"`, reports `goblins notify <parent> --waiting-on <helper> "<why>"` while it only waits, which makes the helper's progress count as its own, and once the helper reports done runs `goblins helper merge <parent>`: a merge commit of the helper's branch in the parent's own worktree, then the helper retired through Stop.
A conflict is left for the parent to resolve and commit, and asked again the command only retires the helper; a parent that opens a pull request stays the one who opens it.
A helper its parent no longer needs is stopped with `goblins kill <helper> --reason "<why>"`, which keeps its unmerged work.

### Goblin names

Every goblin gets a fun first name and title, such as Jerry - Code Designer, from a list in the binary, as soon as its task is queued.
The supervisor names each queued task the board shows and keeps the pair in `state/.goblin-names-queued.json`, and `goblins spawn` gives the task that same pair, so its name is the same in Tasks and In progress.
A task spawned without being queued first gets its pair at spawn.
The name is one no live goblin or queued task holds and none of the last 300 spawns used, from a pool of 452, so recent History never shows one name on two tasks.
Give the title yourself when you know the task best: a `goblin title: <title>` line in the brief's Delivery section, one to three words and at most 20 characters, such as `goblin title: Estimate Elf`, is the goblin's title as written, and `goblins brief ready` refuses one past that rule.
A queued task keeps its name when its brief gives a title later, and its card takes that title.
Without the line, the title names the subject of the work in the task's own words with a fun role, such as Estimate Elf for a cost estimate fix, Corpus Curator for reloading legal corpora or Voice Whisperer for long dictation.
The subject is the word the task's title leans on most, the last word of a phrase counting most, so "cost estimate" is about an estimate; a label before a colon, words in brackets and the id count less, the project the id names never counts, and the brief's Task section is read only when the title and id give no word.
A subject word another live or queued goblin's title already names is passed over while the task names another, so two goblins on different work read apart on the board.
A task that gives no word a title can use gets a generic title, such as Code Designer.
`goblins spawn` prints the pair on a `goblin:` line, the task's record keeps it, and the supervisor gives one to any live goblin that has none, the pair it was queued with where it has one.
The board, the merge train and the orchestration canvas show the name, title and harness icon in place of the task id, a queued task's card included, and the task sits in the panel and the hover tooltip.
`goblins fleet-view`, the wake queue and a merge train's pull request name each goblin as `Name (id)`.
Call goblins by name when you tell the Supreme Overlord about them.
The id stays the handle every `goblins` command takes.

### Naming a project

Every command that takes a project takes a path or a bare name.
A path wins and is used exactly as written: anything with a separator, a drive, or a dot segment is a path, so `.\acme-api` is a directory and `acme-api` is a name.
A bare name is looked up among the folders of the projects root, the one machine setting `goblins install --projects-root <dir>` records.
The first rule with a match decides: the name as written, then the name ignoring case, then the name as the leading words of a folder, so `northwind` finds `Northwind-AI` and `acme` finds `Acme.com`.
The last rule stops at a word boundary (`-`, `_`, `.`, or a space), so `tail` names nothing even with `Tailspin` beside it.
A whole-name match beats a longer sibling: `tailspin` is `Tailspin`, never `Tailspin-staging`.

- The matched folder must hold a `.git`; one that does not is refused.
- A name that matches more than one folder is refused with the folders it matched.
- A name that matches nothing is refused with the projects root and the checkouts it holds.
- With no projects root recorded, a bare name is refused with the `goblins install --projects-root <dir>` that fixes it; a path keeps working.

There is no registry and no mapping file: the folder is the project, and its name is the credential scope on every machine.
`goblins auth store`, `goblins auth list` and `goblins auth copy` name a scope rather than a checkout, so they resolve a bare name the same way when it is one of your checkouts and otherwise keep it as the scope you typed, which is how a scope with no checkout on this machine is still addressed; the `stored <scope>/<NAME>` line always prints the scope that was written.
A folder that matches the name but holds no `.git` is not one of your checkouts, so the store commands keep the name as typed there as well, while every command that needs a checkout still refuses it.
An ambiguous name is refused there too, and so is a projects root that is recorded but cannot be read: whether the name is a checkout is then unknowable, and writing a credential into a guessed scope is the silent failure this refusal exists to prevent.
To reach a scope whose name would now resolve to a checkout, such as one stored under `northwind` before this, write it as a path: `--from projects/northwind`.
## The fleet never idles

Your job is the whole list, not only the goblins in flight.
While memory is free, every paused goblin whose pause cleared resumes and the next queued task starts, until nothing that could run is left; the last goblin retiring is never the end.
The product does this by itself in every home, with no setting, and wakes you for what it cannot do:

- **The supervisor starts the work.** `goblins serve` reads memory once a minute, and at each reading with 5 GB of memory and of commit free it takes one slot: a reported production defect, then the paused goblin highest in the Supreme Overlord's In progress order whose pause cleared, then the top of the queue, with a [root issue](root-issues.md#root-issues) it picked up after every other queued task, unless a goblin paused for memory comes back first.
  A task it starts by itself, a production defect included, joins the bottom of In progress, under every goblin the board lists, and only his Start puts one at the top.
  It starts or resumes a goblin by itself only while memory and commit, less the about 1 GB a new session takes, still meet the floor `goblins gate turns` shows for the next run of the machine's heavy line, so both test slots stay usable: the Supreme Overlord's rule of 2026-11-02, by free memory and no count of goblins (issue 1926).
  Short of that nothing starts, resumes or comes back after a restart by itself, a production defect included, though the CEO still comes back, `memory_ready` waits too, and the scheduler's line, `goblins gate turns` and the tip of the board's memory meter, whose fill stays amber, all say that the work waits to keep the test slots open.
  A line that cannot be read holds nothing, and is said on the board once three readings in a row cannot read it.
  Every other choice between goblins follows his order too: the memory floor and the allowance floor pause the lowest first, and a restart brings the highest back first.
  Slots go by memory alone: no count of goblins holds a start back, however many run.
  Beyond that hold in the memory meter's tip, the board's meters say none of it in words: what it could not start reaches you as the `idle` wake below.
- **A row says what it waits for, and starts when that clears.** A row under `## Queued` waits while its title line carries `blocked-by: <what> - <why>`, and the scheduler starts it by itself at the first reading with memory free after every one of them cleared, with no edit to the row: `until 2026-10-10T00:00Z` (an RFC3339 time, seconds optional) once that time passes, `memory 12 GB` once a reading has that much memory and commit free, a task id once that task's outcome says it delivered, and a GitHub pull request URL once it merged.
  Write a wait in one of those forms, never a word the scheduler cannot read: one it cannot read, such as `blocked-by: quiet-night` where no task has that id, keeps the row waiting, its card says why, and it is work that cannot start, which wakes you.
  So does a wait that can never clear: a task that stopped without delivering, or a pull request closed without merging.
  A hold `tasks-axi hold` put on a row, `(hold: <why>)` of any `hold-kind`, waits too, as `tasks-axi show` reports it held: until `tasks-axi unhold` clears it, or until its `(hold-until: YYYY-MM-DD)` date begins; its card reads **Held:** and the reason.
  A waiting card says what it waits for in place of Queued, such as **Waits for 12 GB free** or **Starts Oct 10, 7:00 PM**.
  Work that must not start until you decide belongs under `## Parked`.
- **Finished work never starts again.** A queued task whose outcome says it delivered, whose last report in its status log, live or archived, was done, or whose pull request, or one of its branch, merged, is refused by the scheduler and by Start, and its card says **Already finished** in place of Start, with the evidence in the tooltip of that status in its panel.
  So is a task you retired, with `goblins cleanup` or `goblins kill`, after its row was queued, whatever its row still says.
  A row has no time of its own, so its brief stands for it: a brief written since the retirement is new work for that id and starts.
  Instead of starting a retired task's row, the scheduler wakes you once with a `stale_row:` notify keyed by the task.
  Queue new work under a new id.
- **The queue cleans itself.** When `goblins cleanup` (with or without `--force-archive`, and through `goblins reap --apply`) retires a task, its row moves from `## Queued` to `## Done` with its detail lines, in one write that keeps every other line as it was, whatever the task last reported, so nothing starts it again from its row.
  The row closes as `done` when the task delivered and as `retired` when it did not, and the supervisor moves any row a delivered task left behind.
  A local-only task opens no pull request, so it is delivered when a done line of its current run names its report, `data/<task>/report.md`, by its full path or its path in the home, and that report is there.
  [goblins notify --report](../commands/notify.md) keeps the report there and records that line.
  A task ruled finished with no pull request is delivered when a done line of its current run names the issue it closed, which [goblins notify --closed](../commands/notify.md) records from its answered decision.
- **Work that cannot start wakes you again.** A start that failed is not retried by itself, and its card says why.
  A brief changed during admission instead produces `start deferred: ...` and is reconsidered on the next scan with its final choices.
  Draft briefs remain held under the [brief readiness contract](../commands/brief.md), do not block ready work behind them and raise no idle-fleet wake.
  While work like that (a failed start or resume, or a row that needs you, such as one whose brief names no project) waits 30 minutes with memory free and nothing started, an `idle` wake keyed `fleet` names each task and why, and it comes again every 30 minutes that lasts.
- **Your turn does not end idle.** When your turn ends with no goblin at work while work that could run waits and memory is free, it is reopened with an `idle` wake keyed `turn` naming the next work and the exact command: Claude Code's turn-end guard reopens it, and for a Codex or pi CEO the supervisor queues the wake from the turn end its native hook reported and types it into its terminal.
  The same next work reopens a turn at most once every 30 minutes.
  It never names a task the supervisor is starting already, which has no task record until its spawn has its worktree.
  Read `goblins fleet-view` first, since the scheduler may already be starting it, then start or resume what has not started, or park a row that must not start.
## Pausing and automatic resume

`goblins pause <id>` records what resumes the goblin before stopping any process.
`--until <RFC3339 time>`, `--until-task <id>` and `--until-pr <GitHub PR URL>` pause it until that time, until that task delivers or until that pull request merges: a dependency the scheduler resumes by itself, so a goblin paused for Codex's weekly reset with `--until <the reset>` resumes at the reset.
Otherwise name the reason with `--reason`: `memory`, `allowance`, `overlord`, `dependency`, `question`, `ci` or `deploy`; a pause that names nothing that resumes it, or two conditions, is refused, and the board's Pause records `overlord`.
Use `--until <RFC3339 reset time>` for allowance, `task:<id>`, `pr:<GitHub PR URL>` or `date:<RFC3339 time>` for dependencies, and `<question id>` for a question.
The Overlord's Restart PC pauses with `--reason restart --until <when this Windows sign-in began>`, a pause that clears in any later sign-in, so the scheduler resumes it once the PC has restarted; it is the supervisor's, never yours to take.
`goblins pause` on a goblin that is already paused takes the new condition in place, so what resumes it can be changed; what its pause stopped and kept stays, and nothing is stopped again.
A pause ends the goblin's terminal first, so a busy goblin that misses its stopping point is still paused, with its session kept for its resume, even when the search for its other processes runs out of time; its record names that search as a problem.
That search, and the sweep a cleanup and a relaunch make, run one priority class above normal for as long as they take and give it back after.
They read every process on the machine in ten seconds, and at the priority of the work they end they wait their turn behind it: on a machine with six goblins building, one such reading took up to 3.9 seconds at normal priority and 0.02 above it, and a memory pause is taken when the machine is at its busiest.
A pause or a stop then ends every process that is the task's own, and a process is the task's own by any of four things: its terminal's job, which holds what the harness started itself.
Its terminal's mark, a value every process started in the terminal inherits and keeps when its parent exits and when Git Bash starts it outside the job.
Its place, at work in the task's folders or running a program from them.
Or its parent, when that is the task's own.
So a browser bridge with its browser, a server left in the background, a watcher, a pipeline or an MCP server ends with its goblin though it detached from the terminal.
What a pause keeps is named, never guessed: the worktree, the branch, the session and the handoff, the open gate run, the machine services, and the desktop programs.
The machine services are Docker Desktop with what it runs, the no-mistakes daemon with every other gate's agents, and the Scrawl server that keeps every goblin's review page, whichever goblin started them.
The desktop programs are the ones the Overlord uses himself though a goblin started them, as when a sign-in opens his browser: a program that shows a window, a browser no tool drives, a packaged desktop app and Explorer, each with what it started.
A gate's agent carries the mark of whichever goblin started the daemon, so it is a task's only while it works in that task's own gate run.
A program one of Git Bash's own tools starts carries no mark when it is such a tool itself, so it is the task's by its folder or its parent alone.
A pause that fails while the goblin's terminal still runs types into it that the pause did not take effect and it is not paused, so a goblin that already wrote its handoff carries on instead of waiting idle at its prompt, and the failed record says whether it was told.
CI and deploy take `--until pr:<GitHub PR URL>@<40-character SHA>` or `run:<GitHub Actions run URL>@<40-character SHA>`; pause only when waiting on that run is the goblin's remaining work.
The supervisor's one scheduler resumes memory after two readings of 5 GB free memory and commit, allowance at reset, a dependency when its task finishes or PR merges or date arrives, a question when the Overlord answers, and CI/deploy on the matching `ci_finished` record, each only while memory and commit, less the about 1 GB its session takes, still meet the floor the machine's heavy line keeps for its next test run.
An Overlord pause needs his Resume, and a legacy pause with no condition also needs manual Resume.
Pausing or stopping a goblin first pauses or stops each of its [helpers](dispatch.md#helper-goblins) through the same path, each with its own record, handoff request and card, a pause keeping the parent's condition, except a helper the parent is paused to wait on, which keeps working; the parent's own sweep then ends anything a helper's operation left.
Resume is one card at a time, a helper's included, since each start needs its own memory; a helper paused with its parent needs no Resume of its own, since the scheduler resumes it once its parent runs again, when memory allows.
Resume keeps the task id, worktree and branch; it continues the saved session less than a day after pausing where supported, then uses the retained handoff.
A goblin that writes no handoff by its stopping point still leaves one: the pause writes it for the goblin before its stop, saying so, with the task's branch and head, its open pull requests, its last status line and its runs in the machine's heavy line, which the stop ends, and its report to you says it did.
Claude goblins kept answering the pause's request with text alone and no tool call, so between 2026-10-16 and 2026-10-22 at least six pauses ended with nothing recorded beyond the worktree (issue #1338).
A handoff the goblin publishes meanwhile is its own, and the pause never writes over it.
A resume the machine has no room for by the time its terminal would start changes nothing: the goblin stays paused as it was, a memory pause still a memory pause, nobody is woken, and the scheduler resumes it before it starts anything new.
A resume its relaunch refuses for anything else before it starts a terminal changes nothing either: the pause, its reason and its condition stay, the task's status log gains one `lifecycle-refused:` line saying why, and no wake reads the task as running.
A relaunch has started nothing until it records the task's replacement session, which it does before it starts a terminal, so a refusal before that is one whatever refused it: a task recorded in Herdr, a saved engine or a paused validation that cannot be read, another validation run on its branch, or a harness that is missing.
The scheduler does not try a refused goblin again by itself: it tells you once, as `resume failed:`, and the goblin waits for a Resume, so it never holds the slot ahead of the queue.
A resume that fails after its relaunch recorded that session is a failed resume, as before.
One whose goblin then reports from that session, working, waiting, asking or done, took its instruction after all, so the supervisor's next reading finishes its record as running and writes one `lifecycle-running:` line saying so.
A resume and a `goblins cleanup` of the same task never run together: whichever starts first holds the task, and the other is refused before it changes anything, with one line saying what holds it.
A resume the scheduler started that the task's cleanup refused is no failure of the goblin's: the task is gone, or goes once the cleanup ends, so the board shows no failed resume and nobody is woken, and a cleanup that was refused in its turn leaves the goblin paused for the scheduler's next reading.
A relaunch also looks at its task once more after its wait for the spawn lock, and one whose task record or worktree went meanwhile stops there, with nothing written and no terminal started.
A pause, whatever its reason, leaves the goblin's open gate run running, and the resumed goblin carries on from it.
Only a stop aborts the run, after keeping its commits.
So a `goblins cleanup` of a paused goblin whose run is still open, or whose gate cannot be read, is refused before it changes anything, with or without `--force-archive` and whatever state its worktree is in, naming `goblins kill <id>`, which aborts the run after keeping its commits and retires the task.
While the task's worktree is gone or no longer a Git worktree, `goblins kill` cannot find the run (issue #2246), so the refusal also names `no-mistakes axi abort --run <run>` with the refused run's id, after which the cleanup is run again.
While only a memory pause left the run alone, pauses for every other reason ended 29 of 147 runs in three days, each of which then started again from its first step (issue #2089).
A goblin authorizes one dependency pause of its own with `goblins notify <id> --prepared-wait task:<id>|pr:<GitHub PR URL> --generation <generation> --handoff <handoff-file>` ([goblins notify](../commands/notify.md)) once its preparation is complete and no worker-owned work remains; a plain `--waiting-on` never authorizes one.
At each fleet reading the supervisor reconciles that record and pauses the goblin only while every piece of evidence still matches the signal: the task's generation, project and worktree, its saved session, its newest report, no prompt since the signal and no message queued for it, its branch and clean head, the saved handoff's digest, no active or parked gate run and no gate custody held, a fresh ownership reading with no job, no wait, no unfinished or newer child and no helper, and a fresh idle reading of its terminal.
The prerequisite must be readable too: an unknown target task, a cycle of prepared or dependency-paused waits, or a pull request that closed without merging or reads as unknown pauses nothing.
Nothing of that is guessed: evidence that cannot be read or is stale, a message still queued for the goblin, or a terminal that does not read idle leaves the signal for the next reading.
A changed generation, worktree, session, branch, head or handoff, a newer report, prompt or working activity, worker-owned work, or another lifecycle operation on the goblin invalidates the signal for good, so only a new signal authorizes a pause again.
Each refusal wakes you once with a `check` wake that names its reason.
When the prerequisite has already cleared, the record reads `cleared`, the goblin keeps running, and one `check` wake says so.
Otherwise the supervisor checks the prerequisite and every piece of evidence once more right before it calls the existing `goblins pause` with `--reason dependency`, the condition and the signal's stable operation id, and it records the admission before the call.
So repeated readings, a replayed signal and a supervisor restart pause the goblin at most once, and the pause keeps its worktree, branch, session and handoff as any dependency pause does.
The scheduler resumes it once its task delivers or its pull request merges, under the operation `resume-<pause operation>`, with one call that is never retried past the spawn lock.
A prepared pause or resume that fails or ends with no matching completed lifecycle record reads `failed` and wakes you to inspect it, and neither the scheduler nor recovery after a restart retries it by itself; the Overlord's own message still brings back a goblin whose prepared pause failed.
A transition the supervisor is still dispatching, or one still running under its controller's lifecycle lock, is left to finish, and a lifecycle record that cannot be read keeps the admission for the next reading.
A prepared pause that another lifecycle operation took over, such as the Overlord's own Resume or a new pause, reads `invalid` with a `check` wake naming that operation, never `failed`, so it holds back no recovery.
Validation identity checks and policy handling on Resume follow the [pipeline resume contract](../pipeline.md#resuming-a-paused-task).
Answers given while paused ride the resume prompt.
For a free slot, the oldest cleared pause comes before the Overlord's queue order; unresolved pauses do not block new work.
His Start or Resume overrides that order, and `(priority: production-defect)` marks his reported production defect to jump both, with a notify saying why.
Start, spawn and Resume share the memory/commit floor: each needs 5 GB of memory and of commit free, which keeps the 4 GB floor, and memory is the only limit on how many goblins run.
They share the disk floor too: none starts while the home's drive has less free than `config/fleet.json`'s `disk_floor_gb`, default 15, and the refusal names what is free and the floor; `goblins gate test` starts no run under it either.
What the supervisor starts by itself also waits for room on the processors: the next queued task, a goblin whose pause cleared, a goblin coming back after a restart and a helper start only once a quarter of the machine's performance cores sat idle since the last reading, so the Overlord's own apps keep room.
Each of them also waits for the memory the machine's heavy line keeps for its next test run, as [the supervisor starts the work](#the-fleet-never-idles) says, so a new session never takes the room a waiting test run needs.
Each install reads its own cores for this: on a processor with performance and efficiency cores only the performance cores count, a core counts as busy while either of its threads is, and on a processor with cores of one kind all of them count.
A goblin's own work also keeps off half of the machine's performance cores, rounded up to whole cores: its terminal and everything started in it run on the threads of the other performance cores and no others, so the Overlord's apps have cores no goblin's build or test takes.
On a processor with cores of one kind all of them count as performance cores, and on one with efficiency cores the goblins' work keeps off those as well, which stay with the system's background work.
Windows keeps a hidden process that asks for nothing on the efficiency cores and does not move that work when they fill: on 2026-11-05, of eight hidden processes of 6 seconds' work each, started together on six performance and four efficiency cores, 1 or 2 finished within the wait with the efficiency cores in the mask and all 8 without them.
Keeping off half of the performance cores costs the fleet speed only while its work fills every core it keeps: on 2026-10-10, with the efficiency cores in the mask, 59% of what it got through on every core, on the six performance cores it was measured on.
Your own terminal keeps every core, and so does a machine with one performance core or with more than 64 processor threads.
The work that waits is named with the reason where the scheduler says what waits, and the `idle` wake tells you of it.
A Start or Resume the Overlord clicks, a reported production defect and a `goblins spawn` you type do not wait on the processors.
The board's Tasks box shows the same reading as **CPU free**, with the two apps using the processor most in its tip, and each graphics adapter as **GPU free**, on which no start waits.
The fleet's own controls run one priority class above normal and never higher, so they answer while goblins fill the cores: the supervisor, each terminal's host, and the commands that are a short conversation with one of them or with the home's own records, which are `status`, `stop`, `attach`, `send`, `answer`, `peek`, `fleet-view`, `drain`, `watch`, `notify`, `question`, `register`, `pause`, `resume`, `kill`, `update`, `process-plan`, `session-start` and every hook from its first act.
Each gives the class back when its work ends, and a process that is not at normal priority is left as it is.
A goblin's harness and everything started in its terminal run at normal, a `goblins` command a goblin runs there included, and so does what a gate agent starts: Windows starts the child of a raised process at normal, and no start in the fleet names a priority class but a terminal's host's and its input waker's.
Those two are started one class above normal, since a host's launch waits on the host's record and the host on its waker: both came to exist at the priority of the work, the two steps of a host's start that grew with the load beside it, and on a busy machine a host missed the launch's 15-second bound (issue #891).
Where goblin work has every core, as on a machine with a single performance core, a control at normal priority waited its turn behind it: beside sixteen busy threads on 2026-10-10, one reading of the machine's processes took 7.7 seconds at normal priority and 0.04 raised, a terminal's handshake 58 milliseconds against 0.2, and one of the five program copies an update's prepare step makes 3.5 seconds against 0.07.
Where goblin work keeps to its own cores, a control that asks Windows not to throttle it, as the supervisor and every host do, answers in milliseconds either way, and the raise is for the control that asks for nothing, such as a pause the supervisor starts, which Windows keeps to the efficiency cores: beside ten busy threads on the goblins' cores, the efficiency cores then among them, such a reading took 0.9 seconds at normal priority and 0.013 raised.
What the raise cannot reach still waits its turn where goblin work has every core: a `goblins` command or a hook takes about 0.7 seconds to come to exist there, because its process is started at its caller's priority, `goblins peek` takes 2 to 3 seconds, because the terminal's console answers each row of the screen at normal priority, and each of the supervisor's progress readings starts git twice, about 0.8 seconds a goblin.
With goblin work kept to its cores, the efficiency cores then among them, those took 0.02, 0.07 and 0.17 seconds.
The board shows disk free under memory and the kernel's pools, in the memory meter's box, and under `disk_wake_gb`, default 10, the supervisor wakes the CEO once with a `disk` wake, and again only after a reading back at or above the floor.
The allowance floor is the percent of a measured weekly window the home keeps back, each provider's own as `config/fleet.json`'s `weekly_floor_percent` sets it and 5 percent where it sets none, which `goblins allowance-floor` reads and sets.
At the floor the scheduler asks for a handoff and records an allowance pause with its weekly reset, the seam AFK mode shares.
The 4 GB memory floor pauses the live goblin lowest in his In progress order not pushing or merging, one at a time, with a `memory` pause the scheduler resumes at 5 GB once a new session leaves the heavy line its next floor, whether or not [AFK mode](afk.md#afk-mode) is on.
It passes over a goblin resumed from a memory pause in the last 10 minutes, and pauses one only when no other goblin may be paused.
Short session or model windows do not trigger this reserve, and missing or stale quota remains unknown.
A session window that is used up, or a week used up under a floor of 0, is waited out rather than paused: until it renews, nothing starts or resumes on its harness, and when it renews one `allowance` wake names the goblins it stopped, since Claude Code goes on by itself only where its screen offered to; peek each and send an idle one on.
One `allowance` wake also comes when a window bounding a running goblin's model passes 85 percent used, once per window and reset.
While a window bounding Codex is used up, every Codex session bills paid credits, and the floor holds only the goblins that run on Codex, so every goblin's terminal carries a codex guard: its PATH begins with its own folder, `state\codex-guard\<id>`, which is empty while Codex has its allowance, so `codex` is the one installed.
At each fleet reading the supervisor arms it while a session or weekly window bounding Codex is used up, with a `codex.cmd` for cmd, PowerShell and every program that searches `PATHEXT` and a `codex` script for Git Bash, and empties it once the window renews; a reading that is skipped or says nothing of Codex keeps the last word until the window it named renews, and a goblin spawned meanwhile is armed at the next reading, within a minute.
An armed codex runs `goblins codex-guard`, which prints one line that says why, starts nothing and exits 1, and the first time a task meets it before Codex renews one `allowance` wake names the goblin and the command, so a rig, a probe or a test that starts `codex exec` costs nothing.
A task the Overlord authorizes to spend credits, which `goblins auth grant <id> --codex-credits` records, is never armed.
A terminal started from a guarded one, as a rig's scratch goblin or scratch CEO is, keeps the guard it was started under behind its own, which `GOBLINS_CODEX_GUARD` names.
The guard holds against a goblin that starts `codex` by name; one that names the program's path or builds a PATH of its own passes it.
The board shows each paused card's reason and what resumes it in place of Paused, Next on the card the free-slot order takes first (a production defect says it jumps the queue), and a stall line on a live card past 20 minutes without progress; a CI or deploy wait names the repository's median measured run.
The board's Paused section holds only goblins whose terminals were stopped for memory, by a `memory` pause or his own Pause, and a goblin paused to wait on anything else stays in In progress with its wait as its status.
A goblin paused until its own pull request merges, or until its CI run on that pull request finishes, reads its pull request's test until its train has landed it, Testing then Landed or what failed, with the same link as its [merge train](delivery.md#merge-trains)'s card, or its own CI's state with that run's link.
The supervisor raises one `progress_stalled` check wake after 20 minutes without a new commit, push, change in its gate run, changed status report, new output on the goblin's screen, transcript write or processor use by its own processes, resets it on any of them, and suppresses it during intentional pauses.
So a goblin inside one long tool call whose child uses the processor, or whose screen fills with output, is working, and one whose screen, transcript and processes all stay still is raised once, then again only after such progress has come and stopped again.
A goblin waiting by design, on a sub-agent, background shell or monitor its harness records open or on a run of its own in the machine's heavy line, is raised only once that wait has shown nothing new for the busy budget (one hour), and the wake then names the wait.
A goblin whose no-mistakes run has a step at work is working while that step's round runs within the time nine in ten rounds of that step have taken on this machine, or within an hour for a step with none on record, whatever the step writes.
Its run is the newest run of the branch its worktree has checked out, or the run it names as its wait with `goblins notify <id> --waiting-on <run-id>`, and a run still going is its own even when it started before a switch gave the goblin a new generation.
A goblin quiet for 20 minutes whose run is itself stuck, its step running past that time or parked on an answer, draws one `gate_stuck` or `gate_parked` check wake naming the run and the step in place of `progress_stalled`.
While memory or commit reads under the 4 GB floor, every read slows, so a goblin whose progress, screen or tree those reads could not reach is not raised and its timed-out read is no `supervisor_error`: its clock waits for a reading that can see it.
Its screen output is the monitor's last look at the screen without the rows the harness redraws by itself (its spinner, its clock and its interrupt hint), and its transcript and processes are the board's last reading of its family tree, each counted only from a reading of the last two minutes, and the wake names what could not be read.
A goblin whose latest report is a wait on its own [helper](dispatch.md#helper-goblins) takes the helper's progress as its own and draws no such wake while the helper is watched, since the helper's own check reports its stall; once the helper is paused, the goblin is checked as any other.
Each pass reads every goblin's progress at once under one 10-second deadline, so a pass of two or more reads in which fewer than half finished in time says the machine was slow, not any goblin: no goblin's read is a `supervisor_error`, and the pass neither counts nor ends the run of failed passes of any goblin whose read ran out.
The reads are not the same size, since a no-mistakes goblin's also asks the gate for its run, so a pass where those reads run out while a plain goblin's finishes is such a pass too when they are most of the pass's reads.
Once such passes have lasted 20 minutes, one `progress_unread` check wake says the stall check has judged no goblin whose read ran out since, and it wakes again only after a pass that read a goblin and finished at least half its reads in time, or had none to read, ended them.
Those 20 minutes count only passes the supervisor ran: a gap of more than two minutes from the end of one reading to the start of the next, such as the supervisor stopped or the machine asleep, starts them again, and a pass the supervisor's own stop cut short counts for nothing.
A goblin whose read keeps running out of time while the others are read is still a `supervisor_error` naming it, once it ran out on three passes in a row.
CI/deploy durations come from start and finish timestamps captured with `ci_finished`; unknown timestamps stay unmeasured, and check names containing `deploy` are classified as deploys.
## Switching a running goblin

`goblins switch` is how a goblin changes harness, model, or effort without losing its place.
It is the one-step replacement for quitting, WIP-committing, cleaning up, and respawning under a new id.

- Nothing is torn down: the task id, worktree, branch, and any open PR all survive, which is why it is safe on work in progress; the new harness starts in a new native terminal under the same id.
- A dirty worktree is refused, except when a goblin whose terminal has ended is resumed in place (see docs/native-board.md).
  Commit first, or pass `--force-dirty` when the mess is deliberate - the handoff then tells the new harness so, and not to tidy it.
- Same harness, new model or effort: Claude Code, Codex and pi resume the task's own conversation by its id (`claude --resume <id>`, `codex resume <id>`, `pi --session <id>`), only when the board's record shows that conversation was this task's goblin of its current generation, or when the goblin's harness proved it is in it: the board reads that proof from the harness's own record while it runs (Claude Code's record of its process, Codex's rollout of the goblin's worktree, pi's session of it) and keeps it for the task's generation, so a pause, a resume and the comeback after a restart still find it once the harness has ended, with no native session hooks installed; without that proof the new harness starts cold with the handoff below.
  A resumed conversation that comes back mid-turn is left to finish it, with nothing typed into it.
- Harness update: `goblins switch <id> --restart` starts the goblin's own harness, model and effort again in place, on its conversation, onto an update of its harness installed since it started.
  The board offers it as **Update** on the goblin's card, and on the CEO's header for the CEO itself, only while one waits and only at the Overlord's press, at the end of the turn (see docs/native-board.md#harness-updates); you never press it, and AFK mode never does.
- Different harness: context cannot cross, so a handoff note is written into the task's tasktmp with the brief path, branch, commits, uncommitted state, and the previous goblin's last status lines.
  The new harness is told to read it first.
- `goblins send` follows the new harness immediately: the switch records it before the new harness starts, and a send reaches the task's terminal through that record.

When a goblin's harness starts being refused by its provider, `goblins fleet-view` shows it as `harness-erroring` and the watcher wakes the CEO with the fault and what to do about it.
A fault in the output of a command the goblin ran, under Claude Code's tool call or Codex's `• Ran`, is the command's, never the harness's.
A warning that a limit nears, such as Claude Code's "You've used 76% of your weekly limit · resets Oct 13, 10am", is no refusal: the harness works on, so it raises nothing.
The standing answers live in `data/routing.json`, as rules of this shape:

```json
{
  "rules": [
    {
      "harness": "codex",
      "fault": "rate-limit",
      "switch": { "harness": "claude", "model": "opus", "effort": "xhigh" },
      "auto": true,
      "force_dirty": true,
      "note": "why this rule stands"
    }
  ]
}
```

`fault` is `rate-limit`, `auth`, or `provider`.
A git-platform (GitHub and friends) rate limit or outage is detected separately as `third-party`: it is the platform's own problem, never a harness-switch case, so no `data/routing.json` rule can answer it and the watcher tells you to wait and retry rather than switch.
A rule with `auto` is a decision already made: run its `goblins switch` the moment you are woken with it, without asking.
Without `auto` it is a recommendation to weigh.
`force_dirty` renders `--force-dirty` in the rule's `goblins switch`, because a goblin that hits a quota refusal mid-work is overwhelmingly likely to have uncommitted changes; without it the delivered command is refused on a dirty worktree and the wake names that.
The watcher never switches a harness itself - it holds the triage singleton, and stalling the whole fleet behind one goblin's relaunch would cost more than the churn it saves.
`goblins doctor` prints the active rules.
The same file holds the execution lanes `goblins spawn` routes through (`lanes`, `default_lane`, `escalate_to`); a project manifest's `routing` block overrides them for that project.
## Dispatch policy

You orchestrate deliberately, never by reflex.

- **Parallel crews.** Dispatch as many goblins as the work calls for and run them concurrently.
  Conflicts are prevented by separation, not serialization: one goblin per repo at a time, and two goblins never share a worktree.
  Order dependent work sequentially; same-repo overlap means queue in `data/backlog.md`, not parallel.
  Independent repos always run in parallel.
- **Every goblin session ends in merged, verified code.** A `ship` session is not complete at a green gate, a pushed branch, or an open PR.
  It is complete when the work is rebased on current `main`, merged, and the merge is verified by reading `main` itself rather than trusting the goblin's report or a PR's status field.
  Verify content, not ancestry: a squash merge leaves the branch's original SHAs unreachable from `main`, so `git branch -r --contains` reports 0 for work that landed perfectly.
  Compare `git diff origin/main HEAD` instead.
  A goblin is retired only after that check passes; anything blocked short of merge stays open with the blocker named.
- **Never spawn what you can answer yourself.** Informational questions ("what does this do", "is this committed") get answered directly from the repo.
  Spawn only for a real code change (ship) or an investigation that needs a standalone report (scout).
- **Classify before you spawn.** `ship` produces a code change and is the default when the request implies one.
  `scout` produces a report and is only for a plan, audit, or diagnosis the Supreme Overlord explicitly asked for, or a question whose answer could change what gets built.
- **Let the lane table choose harness, model, and effort.** Follow [Model routing](dispatch.md#model-routing).
- **Never invent goblins.** Spawn only the goblins the request needs.
  Don't spawn a parallel design exercise beside an implementation you're already confident in.
## Model routing

Load `smart-routing` before dispatch, a routing policy adjustment or an independent helper assignment; it supplies the task and evidence judgments for the lane table below.

`data/routing.json` is the fleet's editable policy for new starts and native helpers.
Installing a build preserves a home's existing table, so the CEO chooses when to adopt a new default.
`goblins doctor` prints the lanes and `goblins route` previews a brief's route.

Without `--harness`, spawn classifies the brief's task, complexity and risk.
Narrow or mechanical work uses `mechanical`, search and reading use `scout`, and ordinary implementation uses `default_lane`.
A lane named for another class takes that class.
High risk, architecture, migration, security, deep complexity and repeated attempts use `escalate_to`, even for a scout.
Constraints, verification, credentials and delivery boilerplate are excluded from the classifier.
The shipped default and escalation use Codex `gpt-6-astra` at `xhigh`, with `gpt-6-luna` at `medium` for small work.
Pass `--harness`, `--model` or `--effort` only for the Supreme Overlord's stated preference.
Explicit choices are recorded as such.
An explicit model stays on the wanted lane's harness rather than being sent to another provider.

Each lane names `harness`, `model`, `effort` and a short `note` saying why it runs.
`fallbacks` maps a wanted lane to an ordered list of other lanes the CEO considers capable of its work.
An escalation lane tries only its declared fallbacks, never an unrelated lighter lane.
Other lanes try their declared fallbacks, then the default and escalation.
The shipped strong fallback is Claude Opus, and the small fallback is Haiku.
No usable candidate means no dispatch.

`start_floor_percent` is the remaining allowance reserved from new starts, 10 in the shipped policy and disabled at 0.
A fresh session, weekly or model window at that floor makes a candidate unavailable before it is exhausted.
Only windows that bound that model count.
Unknown, stale or reset windows do not establish a floor.
The existing weekly reserve still applies.
A fallback records the wanted lane and evidence, and puts a durable notice in the CEO's queue; the goblin's card names its model and effort only in its harness mark's tip.
This changes no running goblin: the separate `rules` array remains the CEO's authority for switching one.

`roles` splits a lane's work into five roles.
The lead plans, decomposes, integrates and decides: it is the lane itself, with that lane's own harness, model and effort and its `fallbacks`, so `roles` never names a lead.
`roles` maps a lane to `implement` (writes code from the lead's plan), `review`, `verify` (both independent of the lead) and `fix` (applies a finding), each an ordered array of lanes: the role's own first, then its fallbacks.
A role runs on its lead's harness and names lanes with explicit models, and a lane with roles names its own explicit model.
A role the lane does not name is its lead's own work, so a table without `roles` routes every role to the lead, as before roles existed.
Implementation and fixes fall back to the lead when none of the lanes they name can run.
Review and verification are kept off the lead's own model: while their lanes name another model, a lane on the lead's model is left out, so when no other is usable the role is refused rather than run on it; they take the lead's model only when every lane they name is that model.
The shipped table names, for Codex work led on Astra, ordinary or escalated, verification on `gpt-6-sol`, with implementation on `gpt-6.1-sol` for ordinary work, leaving review to the lead; for its Claude fallback led on Opus, it names implementation on Sonnet and review and verification on Fable.
A routed spawn chooses the implement role's first usable lane for the lead it starts, after the same quota check, and records it on the task, so a later table edit never changes it.
When that lane is not the lead's own, the goblin hands each bounded implementation slice to a native worker on it, in a fresh context: a Claude goblin's task-local `ceo-implement` agent, or a Codex `spawn_agent` call with that lane's model and effort and `fork_turns: none`.
The worker edits and runs the slice's tests but never stages, commits or pushes; the lead reads its `git diff` and runs those tests before it commits, and owns every commit on the branch.
`goblins spawn` names the lane on its route line as `implement=<lane>`.
A resume on the task's own engine keeps the worker, and an engine the CEO changes leaves the goblin implementing its own work, as an explicit `--harness`, `--model` or `--effort` at spawn does.
A Pi goblin has no native subagent to hand a slice to, so while a Pi lane names an implement role the whole lane table is refused, and every routed spawn, `goblins route` and `goblins doctor` table and every Claude or Codex spawn or switch with native helpers fails, until the role is removed and the Pi lead implements its own work.
Review, verification and fixes do not launch on their roles yet: `goblins route` previews each role's lane after the quota check, or says why a role is refused, and `goblins doctor` prints each lane's roles, while every goblin's gate still reviews, verifies and fixes on its own model.
A project's `routing` block carries no roles, so its lanes keep every role on the lead.

`tiers` orders each harness's lanes strongest first, one lane for each model, and every lane it names runs on that harness with an explicit model.
The shipped table orders Claude as Fable, Opus, Sonnet and Haiku, and Codex as Astra, `gpt-6.1-sol` and Luna; Pi has no tiers until a user orders the models of their own key.
A failed verification moves the role whose work it checked up one tier: when a run's Test step fails and the task's policy hands that failure back to its goblin, as version 7 does, the task's implement worker moves to the next stronger tier on its harness.
A worker whose next tier is its lead's own is retired, and so is one whose model the tiers do not rank, while a worker already on the top tier stays where it is.
The move is recorded on the task with the run that caused it, and as a `role-escalated:` status line naming both lanes, so reading the same run again moves it no further.
Every read of that run says where the role went, so the goblin learns it even when the CEO's respond read the run first.
The goblin implements that correction and every later slice itself, because its session's worker keeps its old model, and a later resume on the task's own engine gives the worker its new lane.
A Lint or CI failure moves no role, and a repeated attempt or newly found complexity still escalates the whole lane through `escalate_to`.
`goblins doctor` prints each harness's tiers.

Every run records what each role spent, so a later routing change stands on measurements rather than guesses.
A run is a task's work from its previous `--done` or `--failed` report, or from its spawn, to the next, and that report appends the run's record as one line of `state/role-measurements.jsonl`.
The lead and its workers are read from the goblin's own transcripts: the lead is its conversation, the implement role is Claude's `ceo-implement` sub-agent or a Codex child on the task's implement lane's model and effort, and every other worker is a helper of its agent type.
Review, verification and fixes are read from the gate's record of each agent call in the runs of the task's branch, and each run's Test step counts as passed or failed for the model of its last test agent.
Each role's row names its harness, model and effort, its sessions or calls, its input tokens with the cache reads among them, its output tokens, its wall time, its review findings and its verification outcomes, and the record lists the run's gate runs and its `role-escalated:` lines.
Nothing is probed or estimated: a gate call that recorded no tokens is counted as untallied, and what cannot be read is named in the record as unmeasured, as the spend before a relaunch in the run is, since only the conversation the task holds now is read.
`goblins doctor` prints each role's totals over every recorded run.

`subagents` maps each harness to `search`, `review` and `verify`, each an ordered array of lane names on that harness.
Search includes reading and summaries, review uses a strong model, and verification prefers the first model different from the parent's recorded model, taking the configured one when the policy names no other.
The shipped Codex verifier uses `gpt-6-sol` at `high`, falling back to Astra when Sol is the parent.
Claude uses Sonnet for verification of Opus work and Opus for verification of Sonnet work.
A configured role without a usable model refuses launch, including an unknown parent model for verification.
A supervisor helper whose Task asks for verification also tries the parent's model last.

For example, these fields refer to lanes defined in the same file:

```json
{
  "default_lane": "build",
  "escalate_to": "deep",
  "start_floor_percent": 10,
  "fallbacks": {"deep": ["strong-claude"]},
  "subagents": {
    "codex": {
      "search": ["mechanical"],
      "review": ["deep"],
      "verify": ["verification", "deep"]
    },
    "claude": {
      "search": ["small-claude"],
      "review": ["strong-claude"],
      "verify": ["verify-claude", "strong-claude"]
    }
  },
  "lanes": {
    "build": {"harness": "codex", "model": "gpt-6-astra", "effort": "xhigh", "note": "ordinary implementation"}
  }
}
```

The example omits the other lane definitions for space.
Every referenced lane must exist in the complete policy.
A project's `routing` block can replace its spawn lanes, while native helper roles come from the fleet policy.
Claude receives task-local `ceo-search`, `ceo-review` and `ceo-verify` agent definitions through `--add-dir`, and `ceo-implement` when the task has an implement lane.
Codex receives its native default helper model and effort settings, plus instructions to use explicit `spawn_agent` model and effort arguments with `fork_turns: none` for each job.
A full conversation fork inherits its parent's model, so routed helpers use a fresh context.
Baby cards show the model in the child's own assistant message or turn context, never the requested model inferred from its parent.
A missing model record is displayed as unknown.
