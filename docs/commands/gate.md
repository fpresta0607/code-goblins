# cfo gate

`cfo gate tests-kept`

The check a no-mistakes repository gate runs from its run worktree: exits 1, listing each one, when the pipeline's own fix commits (subject `no-mistakes(<step>): ...`) deleted a test file, removed a test that HEAD no longer has anywhere, or added a skip, so the run parks with an ask-user finding instead of a test disappearing unseen; approve accepts it, fix restores it.
Skips are judged by code tokens, so skip text inside a string or comment literal (a checker's own test fixture, say) is never a skip, while a real skip that shares its line with such a string still is.
Skips are counted, not matched: per test file, the lines that skip or narrow tests each gate commit's own diff added minus those it removed are summed, following a rename, and a file whose sum is positive while HEAD still has skip lines gate commits added in it is reported with those lines, because the gate's own fixer is the adversary and a later gate commit can reword a skip an earlier one added or move its file.
A goblin's own commits and merges (so main's changes) are its author's call and are never read.
Its first output line names the HEAD it checked, and no-mistakes keeps that line in a park's summary; a gate commit that the head of an earlier park a person approved or skipped reaches is left out and counted, on any branch carrying it.
Approvals come from the no-mistakes database (`sqlite3 -readonly`), not from what was pushed, because pushed is not approved: a parked head pinned or pushed as a backup puts gate commits nobody approved on remote branches.
A commit the rebase step rewrote after the approval is a new commit and parks once more, and when the database cannot be read every gate commit is read and the output says so.
It holds against a fixer that deletes or skips through its own edits, not against an agent that writes the gate's database or commits without the `no-mistakes(` prefix.
This repository's `.no-mistakes.yaml` runs it after lint; another repository adopts it with the same three-line `gates` entry.
CI auto-fix is off in code-goblins (`auto_fix.ci: 0`), and must stay off: a repository gate can run only after rebase, review, test, document or lint, so a no-mistakes(ci) fix that deleted or skipped a test would ship unchecked; a CI failure goes back to the goblin, whose own fix is gated again, and `cfo pipeline run` accepts the lowered count because a repository may lower an automatic fix below the frozen policy, never raise one
