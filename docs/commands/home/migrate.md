# cfo home migrate

`cfo home migrate [--apply --plan <digest>] [--memory-from <dir>]`

Lay out a home whose `data/` predates the layout: create its folders, file every finished task folder and stale brief exactly as the watcher does, give the backlog its Parked section, import a harness memory folder into `data/memory/` once, stamping the index with its source and time, and mark the data laid out so the watcher files it from then on.
Without `--apply` it is a dry run that changes nothing: it lists every file it would move (with its SHA-256), create or change, every task folder it leaves in place and why, and the proof that nothing is dropped, which is the file counts before and after and a digest over the SHA-256 of every file whose content it keeps, taken before and after, which must match; a changed file, such as the backlog, only gains lines.
It ends with `plan: <digest>`, a SHA-256 over every folder the plan creates, file it moves, file it writes with its content hash and task folder it leaves in place with why, and keeps that listing in `state/home-migrate-plans/<digest>.txt`.
`--apply --plan <digest>` plans again from `data/` as it is now, at the dry run's time, and when that plan's digest differs it refuses, changing nothing and backing nothing up, and prints every line in one plan but not the other.
Otherwise it copies the whole `data/` to `state/backups/home-migrate-<time>/data`, each copy verified, refuses without changing anything when what it moves or writes changed since it planned again, makes exactly the plan, and reads `data/` back to prove it; a move that fails stops it, with the moves already made recorded in the filing log and the backlog, and a run after it plans the rest.
`--memory-from` names the memory folder to import, which is only read; by default it is Claude Code's memory folder for the home when that exists, and a fact the home already holds with other content stays as the home has it
