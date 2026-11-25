# Handing a goblin's task to a cloud session

The Supreme Overlord asked on 2026-11-24 whether goblin work can go on while his PC is shut, from a Claude Code session in the cloud.
It cannot reach the board: the home is local and private, and nothing in it is pushed anywhere ([home](../contract/home.md)).
This design adds one narrow way out: a goblin's task handed off through GitHub, worked on by a cloud session that reads nothing of the home, and taken back by the goblin that owns it.

## What exists today

- The board, the task records, the backlog, each brief, report and decisions file, the CEO's memory and every worktree live in the home on the PC.
  The board and the phone's remote access answer only while `goblins serve` runs there ([remote access](../remote-access.md)).
- A cloud session reaches GitHub alone: the branches a goblin pushed, its pull requests, drafts included, and the issues its task claims.
  It runs on Linux in a container that lasts only as long as the session, and by default it pushes only to branches of its own, named `claude/...`.
- A pause already keeps the worktree, branch, session and handoff, and writes a handoff for a goblin that wrote none ([pausing](../contract/dispatch.md#pausing-and-automatic-resume)).
- `goblins issue file` already screens text before it reaches GitHub ([goblins issue file](../commands/issue/file.md)).
  On any repository it refuses credential values, private keys and key-shaped words.
  On a public repository it also refuses paths on this machine, teammates' names, @mentions other than the repository's owner, and the names of the fleet's private projects.

So the work done in the cloud can only arrive as commits on GitHub, and everything a cloud session needs to know about the task must be put there first.

## Goals

1. The Overlord hands one goblin's task off before leaving, with one action on its card or one command.
2. A cloud session given only the pull request's link knows what to do, what is done, what is left and how the task is verified.
3. Nothing the goblin owns changes while it is handed off: its branch, worktree, session and gate run stay as they were.
4. On his return, one action brings the cloud's commits onto the goblin's branch and resumes the goblin, which validates them through its own delivery mode before it reports done.
5. Windows behaviour of every existing command does not change.

## Not goals

- Syncing the home, the board's database or the backlog anywhere.
- Running the supervisor or goblins in a cloud session.
- The cloud session merging anything into the default branch, marking a pull request ready or riding a merge train.
- Starting cloud sessions from `goblins`.
  The Overlord starts one himself from his phone or the web; a later design may add it.

## The flow

1. **Hand off.** `goblins handoff <task>`, or **Hand off** on the goblin's card, on the PC.
2. **Work in the cloud.** The Overlord opens a cloud session on the repository and pastes the prompt the handoff printed.
   The session branches from the goblin's head, works and pushes to its own branch, and opens a pull request whose base is the goblin's branch.
3. **Take it back.** `goblins handoff return <task>`, or **Bring back** on the Command Center item the supervisor raises when it sees that pull request.

## `goblins handoff <task>`

Before it changes anything it refuses, naming why, when:

- the task's frozen delivery mode is `local-only`, since its work may not be published;
- a gate run is active or parked on the branch, since the pipeline owns the branch while it runs ([delivery](../contract/delivery.md));
- the goblin has helpers still working, or is itself a helper;
- the task is already handed off;
- the branch's ready pull request has CI still running, since a push then cancels that run after it billed its minutes ([delivery](../contract/delivery.md));
- the worktree is dirty, unless `--wip`, which commits everything in one commit titled `wip(handoff): <task>`.

Then, in this order, each step recorded so a failure part-way says which ran:

1. Pauses the goblin through the task lifecycle with a new reason, `handoff`.
   While the packet that pause's handoff publishes is out, only `goblins handoff return` or `goblins handoff cancel` clears it: a plain resume, and `goblins switch`, which would relaunch the goblin, are refused, since a second writer on the branch is the harm.
   The pause asks the goblin for its handoff as every pause does, and for this reason the goblin writes it for publication: what is done and what is left, as it would put them in a public issue.
   A goblin that writes none by its stopping point leaves the fallback the pause writes for it, which carries its last status line word for word ([pausing](../contract/dispatch.md#pausing-and-automatic-resume)).
   A handoff never publishes that fallback: it stops here with nothing pushed or written and the task left paused, and a second `handoff` goes on once the goblin has written its own.
   A pause whose handoff was only that fallback published nothing, so the task is not handed off: a plain resume clears it, and no refusal says it was handed off.
2. Pushes the goblin's branch.
3. Opens a draft pull request from it into the default branch, or reuses the goblin's open one.
   A draft starts no hosted CI ([delivery](../contract/delivery.md)).
4. Writes the **handoff packet** into that pull request's body, between `<!-- goblins-handoff v1 -->` and `<!-- /goblins-handoff -->`, keeping the rest of the body:
   - the handoff the goblin wrote for publication, never a fallback the pause wrote for it;
   - the issues the task claims;
   - the branch, its head and the commits since the default branch;
   - the delivery mode and the verification commands frozen in the task's delivery receipt, each marked Windows-only when its package is on the Linux CI job's EXCEPT list ([go workflow](../../.github/workflows/go.yml));
   - the rules for the cloud session: branch from this head, push only to your own branch, open your pull request into this branch, say in its body what you ran and what you could not, never mark anything ready, never merge.

   The packet never copies the task's brief, its decisions or the goblin's status lines verbatim.
   This reverses nothing: the brief itself still never reaches a pull request body, as a gate run's `--no-publish-intent` keeps it out of the one the gate opens ([pipeline](../pipeline.md)).
   On every repository, private or public, the packet passes the screen `goblins issue file` applies to a public repository's issue before anything is written, and the handoff refuses, naming the line, rather than redacting it.
5. Marks the task record `handed_off` with the pull request, whether the handoff opened it, the head and the packet's digest.
6. Prints the cloud prompt: the repository, the pull request link, and one line telling the session to read the packet and follow its rules.

While a task is handed off the supervisor raises no stall, idle or `pr_health` wake for it, and its card shows **In the cloud** with the pull request.
The teammate-overlap read stops while the task is handed off, as it does for any paused goblin, and runs again on return.

## In the cloud

The cloud session needs no `goblins` and no home.
It reads the packet, checks out the goblin's head, makes a branch of its own, works and pushes there, and opens a pull request into the goblin's branch.
Its body says which of the packet's verification commands it ran with what result, and which it could not run, such as the Windows-only tests.
It may push more rounds to its own branch; it never pushes to the goblin's branch.

## Watching for the cloud's pull request

The supervisor already reads open pull requests in watched repositories ([supervision](../contract/supervision.md)).
For a handed-off task it now also looks for open pull requests whose base is the task's branch.
When one appears, or its head moves, it raises one Command Center item, **Cloud work ready**, naming the pull request, its author, its commits and what its body says it verified, with **Bring back** and **Leave it**.
An author other than the account `gh` works as is named on the item, and bringing that pull request back needs the Overlord's press: the fleet never pushes to a teammate's pull request, and bringing it back adds its commits to a goblin's branch.

## `goblins handoff return <task> [--pr <url>]`

1. Finds the cloud's pull request into the task's branch, or takes `--pr`.
   With more than one and no `--pr`, it lists them and changes nothing.
2. Checks that the goblin's branch on GitHub and in its worktree are still at the head the handoff recorded and the worktree is clean, and refuses otherwise, or while the branch's ready pull request has CI still running.
3. Fetches the cloud branch and fast-forwards the goblin's branch to it in the worktree, or merges it when it does not descend from that head.
   A conflict stops the return with the merge aborted, the worktree as it was and the task still handed off.
4. Pushes the goblin's branch, which GitHub reads as the cloud's pull request merged, since its commits are now in its base.
5. For a `no-mistakes` task, closes the draft the handoff itself opened, so the gate opens its own ready pull request ([delivery](../contract/delivery.md)); a ready pull request the handoff reused stays as it is.
6. Clears the `handed_off` mark and resumes the goblin through `goblins resume`, with one more line in its instruction: which commits came from the cloud, the cloud pull request's link, what its body says it ran and could not, and that the task's delivery mode now validates the whole branch before it reports done.

The goblin stays the task's one validation owner, so the cloud's work passes the same gate, tests and review as the goblin's own.
`return` deletes no branch and closes no pull request beyond what the push does and that draft.

`goblins handoff cancel <task>` clears the mark and resumes the goblin without the cloud's work, closing that draft as `return` does; the packet stays in the pull request's body, marked cancelled.

## Records and the board

- The task record gains `handed_off`: the pull request, whether the handoff opened it, the head, the packet's digest, when, and, after return, the cloud pull request and the commits it brought.
- The pause's `handoff` reason shows on the card as **In the cloud**, and the Command Center item as above.
- `goblins status` names handed-off tasks, and the CEO's digest counts them.
- AFK mode never hands off or brings back; both are the Overlord's.

## Tests

- Each refusal before any change, with nothing paused, pushed or written.
- A failure at each step after the pause leaves the task paused with a record of the steps that ran, and a second `handoff` resumes from there.
- On a private repository as on a public one, the packet's screening refuses a credential value, a private key, a key-shaped word, a path on this machine, a teammate's name, an @mention other than the owner and a private project's name.
- A packet built from a task whose brief, decisions and status lines hold marker text carries none of it.
- A pause that ends with the fallback handoff the pause wrote publishes nothing: the handoff stops after the pause with nothing pushed or written, the task stays paused, and a second `handoff` goes on once the goblin has written its own.
- `handoff` and `return` refuse, with nothing pushed, while the branch's ready pull request has CI running.
- `return` fast-forwards, merges, and stops on a conflict with the worktree unchanged.
- `return` refuses when the goblin's branch moved since the handoff.
- For a `no-mistakes` task, `return` and `cancel` close the draft the handoff opened and leave a ready pull request the handoff reused as it was.
- The supervisor raises one item per new head of the cloud's pull request and no stall wake for a handed-off task.
- An author other than the `gh` account needs the Overlord's press.
- Every package builds on Linux, but `cmd/goblins` and `internal/supervisor` are on the Linux CI job's EXCEPT list, so their tests do not run there.
  The handoff's logic and its tests live in a new package, `internal/handoff`, that the Linux job runs, with `cmd/goblins` and the supervisor as thin callers; the existing Windows suite stays green.

## Pull requests

1. The `internal/handoff` package, the task record's `handed_off`, the `handoff` pause reason, and `goblins handoff` and `return` and `cancel` on the command line.
2. The supervisor's watch for the cloud's pull request, the Command Center item, and the card's **Hand off** and **In the cloud**.

## Open questions

- Should a public repository, this one included, allow handoff at all, since the packet sits in a public pull request body?
  The Overlord answered on 2026-11-24: allow it, screened like public issues.
  So a public repository hands off as a private one does, and its packet passes the screening a public issue passes before anything is written, as every packet does.
  It refuses credential values, private keys and key-shaped words, paths on this machine, teammates' names, @mentions other than the owner, and private projects' names.
- Does a pull request a cloud session opens count as opened by the account `gh` works as on the PC?
  It depends on how the Claude GitHub App acts for the user, and the first pull request must read it from a real one rather than assume it.
- Should **Hand off** offer to hand off every working goblin at once, as Restart PC pauses them all?
  This design starts with one at a time.
- Once the Linux port's installer lands ([linux port](2026-11-11-linux-port.md), layer 8), a second machine that stays on could take a task by the same packet, with a `goblins handoff adopt` that spawns a goblin there from it.
  That is a later design.
