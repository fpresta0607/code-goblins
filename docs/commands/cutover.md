# ceo cutover

`ceo cutover --dry-run \| --plan <digest> \| --undo <folder>`

Move an installed home from the command's old name to `ceo`, once, after the build that renamed it is merged.
The CEO runs it as the new build with the fleet paused: it refuses while a supervisor serves the home.
Each of its forms also refuses, before it changes anything, while any program runs from the home's `bin` folder, judged by the full path of the program each running process was started from, and names each with its pid, such as `bin\goblins-window.exe (pid 1234) runs from this home; close it first`, since Windows does not let a running program be replaced or removed.
The home is the one the old build's user setting for it names, or the home this build resolves.

`--dry-run` changes nothing.
It lists every change, grouped by kind, and ends with the plan's digest, `plan: <digest>`:

- **program**: this build becomes the home's `bin\ceo.exe` and `bin\goblins.exe`, with the hook program and the desktop window beside it as `bin\ceo-hook.exe` and `bin\goblins-window.exe`, and the old build's programs are removed.
- **contract**: each file of this build's contract that an install writes into the home, its `AGENTS.md`, `CLAUDE.md`, `docs\contract` and `docs\commands` among them, is written where its content differs, one line each.
  A home that is a code-goblins checkout keeps its own, which git tracks.
- **setting**: each user setting of the old name's, such as the home's and the projects root's, moves to its `CEO_` name, and the change is announced to new processes.
- **harness file**: the hooks and allow rules in each harness's settings call `ceo`, and the files the old build wrote in a harness's folder (its settings' backups and rules, the native hooks' helper and backup, and pi's extension) take the new names, so each harness's native hooks are this build's own.
- **skill**: each skill the old build installed in the shared skills folder becomes this build's skill of the new name, with Claude Code's junction to it.
  In every skill Code Goblins installed, whether its name changes or not, each file whose content differs from this build's is written and each file the skill's owner file lists as the old build's that this build does not ship is removed, one line each, as an install does.
  A file the owner file does not list, such as one you added, is kept.
  A skill folder Code Goblins did not install is kept as it is.
- **scheduled job**: each job named for the old command takes the new name, and each whose definition calls it calls `ceo`.
  Windows' own jobs are never read.
- **credential**: each credential the old build kept in Credential Manager takes the new build's name for it, its secret never leaving Credential Manager, and the folder of the file store it falls back to takes the new name.
- **state file**: the home's markers, the state files the old build named, the CEO's own terminal's files and the handoff notes take the new names, and the identifiers the state's records hold, such as a host's name, are rewritten.
- **task file**: the live task files that call the command, which are the backlog, the memory, the project records, each live task's folder and instructions, the home's scripts and its config, call `ceo`.
- **label**: in each repository a task's ticket was kept in, each label the old build gave a ticket's state takes the new name, and GitHub keeps it on every issue that carries it.
- **listed, not changed**: the files in the other checkouts under the projects root that name the old command, by line, for their owners to change in their own work, the old build's caches outside the home, which this build makes again for itself and which are left for you to delete, and anything kept because what it would become is already there.
- **note**: each browser that opened the board shows the first-open layout and the quick tour once more, and forgets its pane width, font sizes and chat style, which the browser keeps, not the home.

A file is rewritten only where it calls the command, names a setting, a program, a skill or a file the old build named, or, in a JSON record, holds an identifier of the old name's.
Every other word is kept, so a task's id, a branch's name and a sentence about the role keep theirs.
Recorded history is never changed: archives, backups, logs, status logs, audit files, git history, the Overlord's own file and, in a prose file, any line quoted with `>` and any words in double quotes, which is how his words are quoted.

`--plan <digest>` plans again, and when that plan is not the one the dry run printed it refuses and changes nothing.
Otherwise it first keeps in `state\backups\cutover-<time>` everything its undo needs: the content and time of every file it changes or removes, and each setting's, job's and link's value before.
Then it makes the plan one step at a time, recording each step there as it is made, and a step that fails removes what it made itself and undoes every step made before it.
It ends with the command that undoes it, which names this build by its full path rather than `ceo`, since the `ceo` on the path is the home's `bin\ceo.exe`, which the undo removes.
The command is quoted for PowerShell, as `& '<program>' cutover --undo '<folder>'`, so it can be pasted as it is.

`--undo <folder>` undoes the cutover that folder records, every step in the reverse order, restoring each file's content and time exactly, and removes the folder.
It too refuses while a supervisor serves the home or a program runs from its `bin` folder.
