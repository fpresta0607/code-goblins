# cfo uninstall

`cfo uninstall [--dry-run [--json]] [--yes] [--remove-work] [--remove-tools]`

Remove Code Goblins from this machine and leave it as the install found it.
`goblins uninstall`, `cfo install --uninstall` and the window Windows' Installed apps opens (`goblins-window.exe --uninstall`, which `cfo install` registers for both installs) all run this one uninstall.
It first makes a plan from what the machine holds and changes nothing until the person types yes or presses Uninstall.
The plan names what it stops: the window, the board, the CFO's and every goblin's terminal and what they started, each by its pid and start time, and only what runs from the home's folders or carries one of its terminals' proven marks.
It names what it keeps, in `Code Goblins kept` in the user's folder (or the next free `Code Goblins kept 2`): each goblin's work that no remote, tag or other branch holds, as its changed files, a patch and its commits in a git bundle that is verified, with a note on how to bring it back, the work the archive tags cleanup recorded in `state\archive-tags.jsonl` hold, and the home's `data` and `config`.
It names what it removes: the home, every credential stored under `cfo:` in Credential Manager and the file store, the Dev Drive the home's `config\dev-drive.json` says Code Goblins made, by one administrator script that detaches it, frees its letter, deletes its file, its startup task and its folder once empty, and reads each back, what it added to Claude Code, Codex and pi (the allow rules, the board's hooks, its skills, trust entries for the home's folders, its backups and the agents' records of the conversations held in the home's folders, each by name), each goblin's worktree, the branches goblins made, the recorded archive tags and the `info/exclude` lines it marked in the person's checkouts, its shortcuts, Start at login, its Installed apps entry and the window's notification and tray registrations, its folders outside the home, and the environment last.
A Dev Drive the person had keeps all but its `CodeGoblins` folder, and one holding the person's files is left and named.
It names what it leaves: the tools `install.ps1` fetched, as the home's `state\fetched.json` records them, with their skills, PATH entries and the places they made, which `--remove-tools` removes with their own installers.
Each group is found again as it starts and after it runs, so a removal is read back.
A keep that does not complete stops the run before anything that holds that work goes.
Anything left is named in one sentence with how to finish, and while anything is left the program, its PATH entry, the Installed apps entry and the home's records stay, so a second run finishes.
It ends with a fresh plan that must find nothing, and its program, moved beside the home while the home goes, is removed a few seconds after it exits.
`--dry-run` prints the plan and `--json` prints it for a program, `--remove-work` removes the work and data instead of keeping them, and `--yes` asks nothing, as Installed apps' quiet uninstall does.
It refuses inside a Code Goblins terminal, which it would stop with the rest, while its dry run, which stops nothing, runs there, so the CFO can read it on the live machine before a release
