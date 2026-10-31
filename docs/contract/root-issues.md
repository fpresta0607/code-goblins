## Root issues

A root issue is a cause the fleet meets while it runs that the work at hand does not change: a test that fails by chance, a wake that fires for nothing, a command that exits without a word.
It becomes a GitHub issue on the repository it belongs to, labelled `ops-root`, so it is fixed where it starts instead of being carried by hand.
You and every goblin file one with `cfo issue file --project <project> --title <title> --body-file <file>` and name the address it prints in your report, never only mention it.
Write the body for a public repository: what happens, the cause when known, and when it is done, with no private project name, teammate, path on this machine or secret, which the command refuses on a public repository anyway.
It files only on the repository the checkout's `origin` names, and only when the fleet owns it, by the rule the pull request watch keeps: a checkout whose `origin` is another owner's, such as a fork kept with `origin` on its parent, is refused with the remote of the checkout whose repository the fleet owns, when one does, and so is a repository with issues turned off.
A root met again is filed the same way: the command finds the open issue of the same root and adds a dated comment with the new evidence.
For a test, use `--root "test:<package> <name>"` with its full package, such as `test:github.com/fpresta0607/code-goblins/cmd/cfo TestNativeHost`, so a later automatic failed-once sighting has the same key.
A manual issue with a prose key or no marker also takes that sighting when its title names the exact test and its text pairs that test with one package, as `<package> <name>`, `<name> (<package>)` or `<name> in <package>`, using the full package or a path containing `/` relative to that repository; conflicting packages do not match, and an explicit `test:` key still decides which test it belongs to.
A goblin whose terminal carries no GitHub credential cannot file, so its report gives the title and body and you file it.

The fleet files what its own signals already name, through the same command path, its screen and its dedup:
- **A test that failed by chance.** Each test a merge train's passing run saw fail once and pass on its second try is filed on the train's repository, keyed by its suite and name whatever job ran it, once a run.
- **A red default branch.** Each red push run of a repository's default branch that wakes you as `ci_finished` is filed on that repository once its failed jobs are read, keyed by its workflow and those jobs.
- **An error the supervisor meets again.** A line of the supervisor's errors met again an hour or more after it was first met is filed on the repository of the home's own checkout, keyed by its class: the line with its paths, quoted text, hashes and numbers taken out, once a class in each run of the supervisor.
A filing that fails, such as one the screen refuses, reaches you as a `root_unfiled:` check wake with the reason, and you file it by hand.

The fleet picks those issues up by itself, project by project.
Every 30 minutes `cfo serve` reads the open `ops-root` issues of the GitHub repository of each project the home keeps a record for under `data/projects/`, one call a repository, and passes over a project with no checkout here or no GitHub origin.
It takes an issue, oldest first, only when its author is the repository's owner, a member of its organisation or a collaborator, no one is assigned to it, no `cfo:` state label is on it, it is not on record in `state/root-pickups.json` as picked up already, no queued or live task of the project names it to take up, no live task filed it, and no open pull request closes it.
A goblin's `cfo issue file` notes each new issue in its task's temporary directory, because a task given a follow-up files the follow-up's issue after it was spawned and may be taking it up itself; an open pull request GitHub links to close the issue, by a closing keyword such as `Fixes #<number>` or by hand, holds it past the task's end.
The scheduler checks both again before it starts a picked-up task, and refuses the start with a `root_reconciliation:` notify naming the owner while one holds the issue.
Each issue it takes becomes task `root-<repository>-<number>` with a brief built from the issue, its text quoted so no line of it is read as a setting, whose acceptance criteria ask for a fix at the cause, a test that is red before it, and `Fixes #<number>` in the pull request so the issue closes when it merges.
New automatic briefs select `mode: no-mistakes` with unknown surface and risk, since an issue's report does not establish a low-risk change assessment.
The [delivery intake](delivery.md#delivery-intake) contract governs any concrete assessment before dispatch; existing picked-up tasks retain their recorded mode and history.
Its row goes to the bottom of `## Queued` with `(priority: ops-root)`, and the scheduler starts it behind every other queued task, as memory allows.
At most two picked-up issues of one repository wait or run at a time, and a notify starting `picked up:` tells you of each.
An issue on record is never picked up again, even when its task ends without fixing it: dispatch it yourself, or close it.
Every brief says this in its Constraints, as `cfo brief` writes it.
