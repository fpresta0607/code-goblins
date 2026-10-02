# Installing Code Goblins

There are two ways in, and each is one command you can rerun at any time.
Both run `goblins doctor`, which checks every tool and harness the fleet needs, add Code Goblins to the Start menu, and end with the quick start in the same window.
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
5. Installs each missing tool the fleet drives: git and gh with winget, Claude Code and Herdr with their own installers, no-mistakes from the release `install.ps1` pins, and Codex, pi and the axi tools with npm, which needs Node.js.
   no-mistakes is the gate every goblin's work passes, so every machine runs the one release `install.ps1` names.
   The install downloads that release's archive and `checksums.txt` from its GitHub release page, never through GitHub's API, whose limit for anonymous callers failed installs on shared machines.
   It installs no-mistakes only when the archive matches `checksums.txt`, puts it in `%LOCALAPPDATA%\no-mistakes`, where no-mistakes' own installer puts it, adds that folder to your PATH and starts the no-mistakes daemon.
   A download that fails is tried twice more, a few seconds apart; if all three attempts fail, the install says why, goes on with the rest, and names no-mistakes among the installs that did not complete.
   Claude Code is the native build, `claude.exe`, because a native terminal starts it with no shell; a `claude` that is only a script, such as npm's `claude.cmd`, counts as missing, and the install adds `~\.local\bin`, where the native build lives, to your PATH.
   When npm's copy still comes first on your PATH, it warns and prints the command that removes it, `npm.cmd uninstall -g @anthropic-ai/claude-code`.
   Kimi has no scriptable installer, so it prints the manual step instead.
6. Installs the skills of gh-axi, chrome-devtools-axi and no-mistakes at user scope, for Claude Code, Codex and pi.
7. Installs the board's native lifecycle hooks for each of Claude Code, Codex and pi that is installed.
8. Adds Code Goblins to your Start menu, which runs `goblins`, the quick start, in a window of its own.
9. Runs `goblins doctor`, prints what still needs a manual step, then runs the [quick start](#the-quick-start) in the same window.

Rerun it to update: it brings the home's contract, skills and program up to date, and keeps your projects folder and any policy you tuned.
A supervisor still running the previous build keeps running it, since a running program cannot be replaced; the install says so, and `goblins stop`, then `goblins`, restarts it on the new one.

## The quick start

`goblins` with no command is the quick start, from any folder, and it waits on you one step at a time: each shows its default marked, Enter continues, the arrows choose and Esc goes back.
A step you have answered takes its screen with it and leaves one line, a tick, the step's name and its answer, so the window holds what is done and the one step that waits, never the screens before it.
Going back to the choice of agent takes the lines after it with it.
The tick and each agent's mark are drawn in Unicode where the console says it can, as Windows Terminal does, and in marks every console font has elsewhere.

1. It finds the supervisor, or starts it in the background, and prints the board's link; it never opens the board on its own.
   Its first line says which: `Supervisor already running`, or `Supervisor started`.
2. When no CFO runs, it asks Claude Code, Codex and pi, each with its own read-only status command, whether it is installed and signed in, and shows them as one row of tabs, each with the agent's own mark, moved with Left and Right.
   Under the row is the marked agent's state: Ready, Not installed, Sign-in needed, or Sign-in could not be verified.
   Claude Code's tab is marked as recommended, for the best experience, and Codex's and pi's notes say goblin reports do not wake them yet.
3. When the agent you choose is missing, Enter installs it the way the install script does: Claude Code's native build from <https://claude.ai/install.ps1>, Codex and pi with `npm install -g`, which needs Node.js.
   When npm's `claude.cmd` comes before Claude Code's native build on PATH, which a native terminal cannot start, it shows the uninstall command to run instead, on a line of its own, and Enter checks again once you have run it.
4. When nobody is signed in, Enter opens the agent's own sign-in in the same window (`claude auth login`, `codex login`, or pi itself, where `/login` signs in and `/model` picks the provider), and the quick start checks again when it ends.
   You sign in there yourself; nothing is typed for you.
   An installer and a sign-in run on the console's other screen, under a line that says what is running, so what they print leaves with them; one that fails keeps that screen until you have read why.
5. It remembers the agent and starts the CFO in the CFO home, in Herdr, or with `goblins --native` in a native terminal of its own, and says so in one line: `CFO started as Claude Code in` the home, or `CFO already running` and where.
   In a native terminal it answers the CFO's startup dialogs whose answers are known and safe, as a goblin's spawn does: Claude Code's trust in the home, Codex's directory trust and update prompt, and Codex's hook review without trusting the hooks, which stay your decision.
   It reads a dialog's focus by the mark the agent draws, Claude Code's `❯` or the plain `>` it draws in a console that does not announce Unicode.
   It types nothing at a screen it does not know, such as Claude Code's own first-run questions.
   For a dialog it has not answered, in Herdr or after those questions, it says what to choose: Yes at Claude Code's trust dialog, whose first choice, No, exits, and Continue without trusting at Codex's hook review.
6. It ends on one screen with the home and the board's link, which Ctrl+click opens: **Open the CFO terminal**, which Enter takes, or **Open the board**, which B takes.
   Esc there leaves both running and exits.

A CFO that ran in a native terminal and was closed comes back in it on its conversation, and the line reads `CFO back as Claude Code on its conversation`, its id and the terminal; the README's [Everyday commands](../README.md#everyday-commands) say when it starts a new conversation instead.
Later runs skip what is already set up: with the remembered agent ready they go straight to the last screen, and with a CFO running they start nothing.
`goblins setup` shows the choice of agent again, and `goblins --harness codex|claude|pi` names it instead of asking.
A screen nobody can answer, as in a script or the install's own CI run, accepts nothing and says to run `goblins` in a terminal.

A no-mistakes older than the release `install.ps1` pins is updated to it the same way, and a newer one is kept.
The install downloads and verifies the pinned release first, then stops the no-mistakes daemon, replaces the program and starts the daemon again.
no-mistakes refuses to stop its daemon while a gate runs; the install then leaves the older no-mistakes as it is and says to rerun it once no gate runs.
The install only ever updates the no-mistakes in `%LOCALAPPDATA%\no-mistakes`: an older one that comes first on PATH from anywhere else is left alone, and the install names its path and says to run `no-mistakes update` or remove it.
So when a Code Goblins release moves the pin forward, rerunning the install is how a machine moves to the no-mistakes it names.

## On a fresh PC

`cfo.exe` is not code-signed yet, so Windows knows it only as an unknown program from an unknown publisher.
Its file properties (right-click, Properties, Details) name the product Code Goblins and its version, and it asks for no administrator rights.

- **The one-line install** runs nothing unless the downloaded `cfo.exe` matches the release's `SHA256SUMS`.
  Windows PowerShell does not mark that download as coming from the internet, so SmartScreen does not prompt.
- **A `cfo.exe` saved from a browser** is marked, and SmartScreen stops it with "Windows protected your PC" and an Unknown publisher.
  Check it first: `(Get-FileHash .\cfo.exe -Algorithm SHA256).Hash` must equal the first field of the release's `SHA256SUMS` line for `cfo.exe`, in any letter case.
  Then **More info**, **Run anyway** runs it.
- **Smart App Control**, where it is on (Windows Security, App & browser control), blocks unsigned programs that Microsoft does not already know, with no way to run them; a signed release is the fix.
- **Microsoft Defender** can send a new build to Microsoft for cloud analysis, which is on by default, and a machine-learning false positive then quarantines it hours after it installed cleanly; `Trojan:Script/Wacatac.C!ml` is the one seen so far.
  Do not add an exclusion or turn protection off.
  Check the file's SHA256 as above, report it to Microsoft as a false positive at <https://www.microsoft.com/wdsi/filesubmission>, and rerun the install once Microsoft clears it or a newer release is out.

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
