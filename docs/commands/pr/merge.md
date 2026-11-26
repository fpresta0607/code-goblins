# goblins pr merge

`goblins pr merge <url> [--method <m>] [--delete-branch] [--verified "<what verified it>"]`

Merge a PR (merge, squash, or rebase).
Where its base requires GitHub's merge queue it adds the PR to the queue instead, pinned to the head it verified, with the queue's own merge method and never `--admin`: the queue tests it on the base's tip with the PRs ahead of it and merges it when that passes, a PR already queued is reported and left alone, and `--delete-branch` is refused because the merge comes later.
In a project whose record declares `delivery.pull_requests_land_held`, it refuses a PR that never carried the `hold` label, as the [merge-train contract](../../contract/delivery.md#merge-trains) says, and a project record it cannot read merges nothing.
The record is that of each checkout under the projects root whose origin is the PR's repository, however its folder is named.
`--delete-branch` deletes the remote ref first and the local branch second, independently: a branch left behind is a warning, not a failure, because the merge has already landed.
The local half only touches a local branch that is the commit GitHub merged, and stays silent otherwise; when it does run on a goblin branch, expect it to warn - git will not delete a branch its worktree still holds.
While [AFK mode](../../contract/afk.md#afk-mode) is on the merge is your own merge word: it needs `--verified`, refuses `--delete-branch`, a pull request that is not a goblin's and, where the base has no merge queue, a head that does not hold its base's tip, and is logged with its evidence before anything merges or joins the queue
