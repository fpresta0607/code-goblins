# ceo cutover

`ceo cutover --dry-run \| --plan <digest> \| --undo <folder>`

Move an installed home from the command's old name to `ceo`, once, after the build that renamed it is merged.
The CEO runs it as the new build with the fleet paused: it refuses while a supervisor serves the home.
The home is the one the old build's user setting for it names, or the home this build resolves.

`--dry-run` changes nothing.
It lists every change, grouped by kind, and ends with the plan's digest, `plan: <digest>`:

- **program**: this build becomes the home's `bin\ceo.exe` and `bin\goblins.exe`, with the hook program beside it as `bin\ceo-hook.exe`, and the old build's programs are removed.
- **setting**: each user setting of the old name's, such as the home's and the projects root's, moves to its `CEO_` name, and the change is announced to new processes.
- **harness file**: the hooks and allow rules in each harness's settings call `ceo`, and the files the old build wrote in a harness's folder (its settings' backups and rules, the native hooks' helper and backup, and pi's extension) take the new names, so each harness's native hooks are this build's own.
- **skill**: each skill the old build installed in the shared skills folder becomes this build's skill of the new name, with Claude Code's junction to it.
  A skill folder Code Goblins did not install is kept as it is.
- **scheduled job**: each job named for the old command takes the new name, and each whose definition calls it calls `ceo`.
  Windows' own jobs are never read.
- **credential**: each credential the old build kept in Credential Manager takes the new build's name for it, its secret never leaving Credential Manager, and the folder of the file store it falls back to takes the new name.
- **state file**: the home's markers, the state files the old build named, the CEO's own terminal's files, the handoff notes and the caches outside the home take the new names, and the identifiers the state's records hold, such as a host's name, are rewritten.
- **task file**: the live task files that call the command, which are the backlog, the memory, the project records, each live task's folder and instructions, the home's scripts and its config, call `ceo`.
- **label**: in each repository a task's ticket was kept in, each label the old build gave a ticket's state takes the new name, and GitHub keeps it on every issue that carries it.
- **listed, not changed**: the files in the other checkouts under the projects root that name the old command, by line, for their owners to change in their own work, and anything kept because what it would become is already there.
- **note**: each browser that opened the board shows the first-open layout and the quick tour once more, and forgets its pane width, font sizes and chat style, which the browser keeps, not the home.

A file is rewritten only where it calls the command, names a setting, a program, a skill or a file the old build named, or, in a JSON record, holds an identifier of the old name's.
Every other word is kept, so a task's id, a branch's name and a sentence about the role keep theirs.
Recorded history is never changed: archives, backups, logs, status logs, audit files, git history, the Overlord's own file and, in a prose file, any line quoted with `>` and any words in double quotes, which is how his words are quoted.

`--plan <digest>` plans again, and when that plan is not the one the dry run printed it refuses and changes nothing.
Otherwise it first keeps in `state\backups\cutover-<time>` everything its undo needs: the content and time of every file it changes or removes, and each setting's, job's and link's value before.
Then it makes the plan one step at a time, recording each step there as it is made, and a step that fails undoes every step made before it.
It ends with the command that undoes it.

`--undo <folder>` undoes the cutover that folder records, every step in the reverse order, restoring each file's content and time exactly, and removes the folder.
