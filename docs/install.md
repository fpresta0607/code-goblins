# Installing Code Goblins

There are two ways in, and each is one command you can rerun at any time.
Both run `goblins doctor`, which checks every tool and harness the fleet needs, add Code Goblins to the Start menu, and end with the guided terminal quick start.
Both put `cfo` and `goblins` on your PATH; the one-line install's own window has them at once, and any other terminal that was already open finds them once you open a new one.

## To use it

```powershell
irm https://github.com/fpresta0607/code-goblins/releases/latest/download/install.ps1 | iex
```

It needs no clone and no Go, and `goblins` works in the same window as soon as it finishes.
In order, it:

1. Stops before changing anything when git or gh is missing and winget, which installs them, is missing too; the fix is App Installer from the Microsoft Store.
2. Downloads `cfo.exe` from the release this `install.ps1` was published with, so the script and the program are always one release's, and refuses it unless it matches the release's `SHA256SUMS`.
3. Asks once for [your projects folder](#your-projects-folder).
4. Sets up the CFO home at `%LOCALAPPDATA%\CodeGoblins`: the CFO's contract, its skills, the default policy, and the program as `cfo.exe` and `goblins.exe`.
   `CFO_HOME` and the home's place on your PATH are set for your user, and the CFO's hooks are merged into your `~/.claude/settings.json`, which is backed up first and keeps your own hooks.
5. Installs each missing tool the fleet drives: git and gh with winget, Herdr and no-mistakes with their own installers, and Claude Code, Codex, pi and the axi tools with npm, which needs Node.js.
   Kimi has no scriptable installer, so it prints the manual step instead.
6. Installs the skills of gh-axi, chrome-devtools-axi and no-mistakes at user scope, for Claude Code, Codex and pi.
7. Installs the board's native lifecycle hooks for each of Claude Code, Codex and pi that is installed.
8. Adds Code Goblins to your Start menu, which runs `goblins` in a visible terminal.
9. Runs `goblins doctor`, then starts the guided agent selection and sign-in flow.
   The final screen offers Enter for the CFO terminal, or B and a Ctrl+click link for the board; the browser opens only when you choose it.

Rerun it to update: it brings the home's contract, skills and program up to date, and keeps your projects folder and any policy you tuned.
A supervisor still running the previous build keeps running it, since a running program cannot be replaced; the install says so, and `goblins stop`, then `goblins --board`, restarts it on the new one.

## Quick start and recovery

Run `goblins` from any folder.
It checks Claude Code, Codex and pi with their own authentication checks, recommends a usable agent, and remembers your choice only after verification.
Use Up and Down to choose, then Enter to accept the marked default.
If the agent is missing, Enter offers its pinned npm installer, shared with this release's install script through `goblins setup --installers`.
Node.js is required for that installer; if it is missing, setup prints its install command.
If sign-in is needed, Enter opens the agent's login and waits for you to finish it.
For pi, use `/login`, choose the provider with `/model`, and `/quit` to return for verification.
An unavailable authentication check is shown as unverified, never as signed in.
Escape returns to the agent choice; interrupted or failed steps can be retried.

The CFO starts in the Code Goblins home, where it works across your projects and files.
There is no project picker or option to continue without a CFO.
Review any workspace trust or hook prompt in the CFO terminal using the guidance printed above the final choices.
Setup never accepts a trust prompt or enters credentials for you.
Enter opens the terminal by default; B or Ctrl+click on the board address opens the board.
`goblins --board` explicitly asks for the browser after starting or finding the CFO.
Later launches keep a running CFO and skip completed setup; `goblins setup` repeats the agent selection deliberately.

Run `goblins resume` after a reboot, or when the CFO's terminal freezes.
It interrupts a running CFO response, replaces that CFO's terminal process, and resumes the exact recorded conversation under the same terminal ID.
The board's **Restart CFO** button offers the same operation with a confirmation first.
Recovery refuses to stop a process whose identity or conversation cannot be verified.
It then checks each task record, leaves running goblins alone, and resumes ended sessions with their existing worktree, harness, model and effort.
Each task is reported as **Resumed**, **Already running**, **Waiting for memory**, or **Needs a hand**, with a next action when it cannot resume.
A restart needs 4 GB free; each additional goblin needs 5 GB, checked again before each start.
Missing session records or unavailable terminals stay visible for manual recovery; no replacement conversation is silently created.

## To work on it

```powershell
git clone https://github.com/fpresta0607/code-goblins.git
cd code-goblins
.\install.cmd -Dev
```

It needs Go: `winget install -e --id GoLang.Go`.
`install.cmd` runs `install.ps1` with PowerShell's execution policy bypassed, so it works whatever execution policy is set locally.
An execution policy set by Group Policy still applies, and can refuse it.
Type it in full: in PowerShell, `.\install -Dev` runs `install.ps1` itself, which the default execution policy refuses.

`-Dev` does everything the one-line install does, with the clone in place of the download:

- It builds `cfo.exe` from the clone and puts it beside itself as `goblins.exe`.
  A copy still running, such as a supervisor or a terminal's host, cannot be overwritten, so it moves aside to a `cfo.exe.<id>.old` or `goblins.exe.<id>.old` of its own and is removed once nothing runs it, on this run or a later one.
- The clone becomes the CFO home, on your PATH; open a new terminal to use it, since `install.cmd` runs in a PowerShell of its own.
  While another CFO home is in use, such as one the one-line install set up, it refuses before changing your environment or settings; run `goblins uninstall` from that home first, then run it again.
- It makes `.claude\skills` a junction to `.agents\skills`, so Claude Code sees this repository's skills; [load-map.md](load-map.md) shows where each harness looks for skills.

Rerun it after you pull, to rebuild.

## Where your data lives

The fleet keeps its state and data in the CFO home: `%LOCALAPPDATA%\CodeGoblins` for the one-line install, and the clone itself for `-Dev`, in its `state` and `data` folders, which git ignores.
The home is outside every project repository: goblins work in git worktrees of your checkouts, in each checkout's ignored `.worktrees` folder, and the fleet's own state and data stay in the home.
It all stays on your machine: Code Goblins needs no backup repository, account or service for it.
Backing the home up, for example its `data` folder to a private git repository, is only your own choice.
`goblins uninstall` removes the hooks, the environment and the Start-menu shortcut the install set, and keeps the home folder, with its state and data, until you delete it.

## Your projects folder

The install asks once for the folder that holds your checkouts, wherever you keep them.
It is recorded on your machine as `CFO_PROJECTS_ROOT`, beside `CFO_HOME`, and never in this repository, so every adopter's layout stays their own.
With it set, `--project` takes a bare name as well as a path: `--project my-project` is `<dir>\my-project`, matched without regard to case, and the fleet works in that one checkout instead of cloning a second copy.
It is optional: without it every command still takes a path, and `goblins doctor` tells you it is unset.
To record or change it later, run `goblins install --projects-root <dir>`, in the clone for a `-Dev` install; a rerun of either install keeps the folder already recorded.
