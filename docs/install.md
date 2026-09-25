# Installing Code Goblins

There are two ways in, and each is one command you can rerun at any time.
Both end with `goblins doctor`, which checks every tool and harness the fleet needs.
Both put `cfo` and `goblins` on your PATH; a terminal that was already open finds them only once you open a new one.

## To use it

```powershell
irm https://raw.githubusercontent.com/fpresta0607/code-goblins/main/install.ps1 | iex
goblins
```

It needs no clone and no Go, and `goblins` works in the same window as soon as it finishes.
In order, it:

1. Stops before changing anything when git or gh is missing and winget, which installs them, is missing too; the fix is App Installer from the Microsoft Store.
2. Downloads the latest release's `cfo.exe` and refuses it unless it matches the release's `SHA256SUMS`.
3. Asks once for [your projects folder](#your-projects-folder).
4. Sets up the CFO home at `%LOCALAPPDATA%\CodeGoblins`: the CFO's contract, its skills, the default policy, and the program as `cfo.exe` and `goblins.exe`.
   `CFO_HOME` and the home's place on your PATH are set for your user, and the CFO's hooks are merged into your `~/.claude/settings.json`, which is backed up first and keeps your own hooks.
5. Installs each missing tool the fleet drives: git and gh with winget, Herdr and no-mistakes with their own installers, and Claude Code, Codex, pi and the axi tools with npm, which needs Node.js.
   Kimi has no scriptable installer, so it prints the manual step instead.
6. Installs the skills of gh-axi, chrome-devtools-axi and no-mistakes at user scope, for Claude Code, Codex and pi.
7. Installs the board's native lifecycle hooks for each of Claude Code, Codex and pi that is installed.

Rerun it to update: it brings the home's contract, skills and program up to date, and keeps your projects folder and any policy you tuned.

## To work on it

```powershell
git clone https://github.com/fpresta0607/code-goblins.git
cd code-goblins
.\install.cmd -Dev
```

It needs Go: `winget install -e --id GoLang.Go`.
`install.cmd` runs `install.ps1` with PowerShell's execution policy bypassed, so it works whatever the policy is.
Type it in full: in PowerShell, `.\install -Dev` runs `install.ps1` itself, which the default execution policy refuses.

`-Dev` does everything the one-line install does, with the clone in place of the download:

- It builds `cfo.exe` from the clone and puts it beside itself as `goblins.exe`.
  A copy still running, such as a supervisor or a terminal's host, cannot be overwritten, so it moves aside to `cfo.exe.old` or `goblins.exe.old` and is removed once nothing runs it, on this run or the next.
- The clone becomes the CFO home, on your PATH; open a new terminal to use it, since `install.cmd` runs in a PowerShell of its own.
- It makes `.claude\skills` a junction to `.agents\skills`, so Claude Code sees this repository's skills; [load-map.md](load-map.md) shows where each harness looks for skills.

Rerun it after you pull, to rebuild.

## Where your data lives

The fleet keeps its state and data in the CFO home: `%LOCALAPPDATA%\CodeGoblins` for the one-line install, and the clone itself for `-Dev`, in its `state` and `data` folders, which git ignores.
The home is outside every project repository: goblins work in git worktrees of your checkouts, in each checkout's ignored `.worktrees` folder, and the fleet's own state and data stay in the home.
`goblins uninstall` removes the hooks and the environment the install set, and keeps the home folder, with its state and data, until you delete it.

## Your projects folder

The install asks once for the folder that holds your checkouts, wherever you keep them.
It is recorded on your machine as `CFO_PROJECTS_ROOT`, beside `CFO_HOME`, and never in this repository, so every adopter's layout stays their own.
With it set, `--project` takes a bare name as well as a path: `--project my-project` is `<dir>\my-project`, matched without regard to case, and the fleet works in that one checkout instead of cloning a second copy.
It is optional: without it every command still takes a path, and `goblins doctor` tells you it is unset.
To record or change it later, run `goblins install --projects-root <dir>`, in the clone for a `-Dev` install; a rerun of either install keeps the folder already recorded.
