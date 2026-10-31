# cfo home move

`cfo home move [--to <dir>] [--apply --plan <digest>]`

Move the home `CFO_HOME` names, such as a code-goblins checkout an older build made its home, to `%LOCALAPPDATA%\CodeGoblins` or `--to`.
Its `state`, `data`, `config` and `caches` move by rename on one drive, a folder holding nothing the checkout's git tracks as a whole, so a move needs no room for a second copy; a tracked file is copied so the checkout keeps its source; the task records' paths into the moved folders follow them, and worktrees stay where their records say.
Without `--apply` it is a dry run that changes nothing: for each folder the files and bytes, every record it rewrites, the older build's programs it removes, the proof that nothing is dropped (a digest over the SHA-256 of every file it keeps, before and after) and `plan: <digest>`, with every move and its SHA-256 listed in `<new home>\home-move-plans\<digest>.txt`.
`--apply --plan <digest>` refuses while the supervisor or any goblin's terminal runs from the home, and when the plan made again at the dry run's time is not `<digest>`, changing nothing; otherwise it makes the plan, reads every moved file back and proves the digest unchanged, installs this build into the new home, which points `CFO_HOME` and PATH there and takes the old home off PATH, and removes the older build's programs.
The CFO's hooks follow at its next start, which writes them for the home it starts in.
Run it at a quiet point, with the supervisor stopped and no goblin running
