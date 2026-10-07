# Installing Code Goblins

`CodeGoblinsSetup.exe` and the one-line PowerShell install are the same install, and you can rerun either at any time to update.
Each puts the Code Goblins app and the `cfo` command line (also called `goblins`) together in one folder, adds that folder to your PATH and Code Goblins to the Start menu, installs the tools the goblins use where they are missing, and opens the app.
Use the setup if you want a window, the one line if you live in a terminal.

## To use it

Download [`CodeGoblinsSetup.exe`](https://github.com/fpresta0607/code-goblins/releases/latest/download/CodeGoblinsSetup.exe) from the latest release and open it, or run this in any PowerShell window:

```powershell
irm https://github.com/fpresta0607/code-goblins/releases/latest/download/install.ps1 | iex
```

It needs no clone and no Go, and `goblins` works in the same window as soon as it finishes.

The setup first says what it will install, where, and what it changes, and starts when you press **Install** (or **Update**, where Code Goblins is already there).
It downloads the install script of its own release and runs it out of sight; the one-line install runs that script in your window.
Either way you see the same four steps, `[1/4] Download Code Goblins`, `[2/4] Check the download`, `[3/4] Install Code Goblins and its tools` and `[4/4] Open Code Goblins`, with a line under the third saying which tool it is installing, a `Note:` line for anything to know, and nothing else.
Every other detail goes to the install's log, `CodeGoblinsInstall.log` in your temp folder.
When something stops the install, it says in one sentence what happened and what to do, and names the log; the setup offers **Show details**, **Open the log** and **Try again**.
Closing the setup ends the install, and running it again finishes it.
The install asks nothing: the app's first-run page starts the CFO and records [your projects folder](#your-projects-folder).

The setup draws at your display's own scale, so its text is sharp at 100, 150 or 200 percent.

### Which home it installs

The install never stops to ask you to run anything first; it decides by what it finds.

| On your PC | What the install does |
| --- | --- |
| Nothing yet | Installs into `%LOCALAPPDATA%\CodeGoblins`. |
| An earlier install | Updates it in place and keeps your settings, policy and fleet. |
| The command line only | Updates it and adds the app and its Start menu entry. |
| `CFO_HOME` naming another folder that holds a fleet, such as a clone an older build made the home | Keeps using that folder and says so in one line: the programs go into its `bin`, those at its root are brought up to date too, and in a clone the files git tracks are left as they are. Moving it to the standard folder is [`cfo home move`](#where-your-data-lives), whenever you choose. |
| The board, the CFO or goblins running | Restarts only the board on the new build, on the address it served; no goblin's or the CFO's terminal is touched, and a build that does not start gives the board back to the one before. |
| An install that stopped part way | Finishes it: every step is safe to run again. |

The install never moves, deletes or rewrites a fleet's state, data or worktrees.

Either way the steps are the script's.
In order, it:

1. Stops before changing anything when git or gh is missing and winget, which installs them, is missing too; the fix is App Installer from the Microsoft Store.
2. Downloads `cfo.exe`, and the desktop window `goblins-window.exe` when the release lists one, from the release this `install.ps1` was published with, so the script and the programs are always one release's, and refuses each unless it matches the release's `SHA256SUMS`.
3. Sets up the CFO home it picked, as [the table above](#which-home-it-installs) says, normally `%LOCALAPPDATA%\CodeGoblins`: the CFO's contract, the default policy, the program as `bin\cfo.exe` and `bin\goblins.exe`, and the desktop window beside them as `goblins-window.exe` where the release ships one, with the `state`, `data`, `worktrees`, `scratch` and `caches` folders beside `bin`.
   The skills Code Goblins ships go once into `~\.agents\skills`, which Codex and Pi read, with a junction to each from Claude Code's skills folder, and `state\harnesses.json` records where each harness keeps its configuration; nothing in a harness folder that Code Goblins did not put there is changed.
   `CFO_HOME` and the home's `bin` on your PATH are set for your user, and the CFO's hooks are merged into your `~/.claude/settings.json`, which is backed up first and keeps your own hooks.
4. Installs each missing tool the fleet drives: git and gh with winget, Claude Code with its own installer, no-mistakes from the release `install.ps1` pins, and Codex, pi and the axi tools with npm, which needs Node.js.
   Each npm package is installed at the exact version `install.ps1` pins, so a new release upstream reaches an install only once it is pinned there.
   no-mistakes is the gate every goblin's work passes, so every machine runs the one release `install.ps1` names.
   The install downloads that release's archive and `checksums.txt` from its GitHub release page, never through GitHub's API, whose limit for anonymous callers failed installs on shared machines.
   It installs no-mistakes only when the archive matches `checksums.txt`, puts it in `%LOCALAPPDATA%\no-mistakes`, where no-mistakes' own installer puts it, adds that folder to your PATH and starts the no-mistakes daemon.
   A download that fails is tried twice more, a few seconds apart; if all three attempts fail, the install says why, goes on with the rest, and names no-mistakes among the installs that did not complete.
   Claude Code is the native build, `claude.exe`, because a native terminal starts it with no shell; a `claude` that is only a script, such as npm's `claude.cmd`, counts as missing, and the install adds `~\.local\bin`, where the native build lives, to your PATH.
   When npm's copy still comes first on your PATH, the closing note names claude, and the log has the command that removes it, `npm.cmd uninstall -g @anthropic-ai/claude-code`.
   Neither Herdr nor Kimi is installed or checked: every goblin and the CFO run in native terminals, and the fleet runs no Kimi for now.
5. Installs the skills of gh-axi, chrome-devtools-axi and no-mistakes at user scope, for Claude Code, Codex and pi, through the version of the skills CLI `install.ps1` pins.
   A skill whose install fails is tried once more; one that fails again is named in the closing note, and the rest of the install goes on.
6. Installs the board's native lifecycle hooks for each of Claude Code, Codex and pi that is installed.
7. Adds Code Goblins to your Start menu.
   Where this install put the desktop window in the home it starts `goblins-window.exe` alone, which opens the app with no terminal: it runs `goblins --window` out of sight, which finds or starts the supervisor and opens the board in the window.
   Where the home only kept a window it already held, as an install from a release that ships none leaves it, the entry runs `goblins --window` itself, with its console minimized: a window from before this may not open the app when started alone.
   In a home with no window it runs `goblins`, the quick start, in a window of its own.
8. Runs `goblins doctor` into the log, names any tool it could not install in one line, and opens the app; a home with no app runs the [quick start](#the-quick-start) in a window of its own.

Rerun it to update: it brings the home's contract, skills and program up to date, and keeps your projects folder and any policy you tuned.
A running program cannot be replaced, only renamed, so the previous build moves aside and the new one takes its name; a supervisor that runs is then restarted on the new build, as `cfo update` restarts it.
An open desktop window likewise keeps running the previous window until you quit it from its tray icon, and Code Goblins in the Start menu then opens the new one.

An install that delivers its desktop window into the home takes the place of a copy that was installed on its own, in `%LOCALAPPDATA%\CodeGoblinsWindow` with **Code Goblins Window** in the Start menu.
That entry is removed, since Code Goblins opens the window now, and Start at login starts this home where it started that copy.
The copy itself, the program and the picture of its notifications, is removed once no window runs from it: one that is open is named and left, and the next install removes it.
Anything else in that folder is left where it is, and so is the window's WebView2 profile, `%APPDATA%\goblins-window.exe`, which both copies use, so the board keeps its layout.
An install that only retains the home's existing window keeps the standalone program, picture, folder and Start-menu entry unchanged.
When it adopts the standalone copy's existing Start at login entry, that entry runs the home's `goblins --window --background`.

## The quick start

`goblins` with no command is the quick start, from any folder, and it waits on you one step at a time: each shows its default marked, Enter continues, the arrows choose and Esc goes back.
A step you have answered takes its screen with it and leaves one line, a tick, the step's name and its answer, so the window holds what is done and the one step that waits, never the screens before it.
Going back to the choice of agent takes the lines after it with it.
The tick and each agent's mark are drawn in Unicode where the console says it can, as Windows Terminal does, and in marks every console font has elsewhere.

1. It finds the supervisor, or starts it in the background, and prints the board's link; it never opens the board on its own.
   Its first line says which: `Supervisor already running`, or `Supervisor started`.
2. When no CFO runs, it asks Claude Code, Codex and pi, each with its own read-only status command, whether it is installed and signed in, and shows them as one row of tabs, each with the agent's own mark, moved with Left and Right.
   Under the row is the marked agent's state: Ready, Not installed, Sign-in needed, or Sign-in could not be verified.
   Claude Code's tab is marked as recommended, for the best experience, and Codex's and pi's notes say what a CFO in them gets: woken by a typed line, with no digest or guards, and for pi no resume.
3. When the agent you choose is missing, Enter installs it the way the install script does: Claude Code's native build from <https://claude.ai/install.ps1>, Codex and pi with `npm install -g`, which needs Node.js.
   When npm's `claude.cmd` comes before Claude Code's native build on PATH, which a native terminal cannot start, it shows the uninstall command to run instead, on a line of its own, and Enter checks again once you have run it.
4. When nobody is signed in, Enter opens the agent's own sign-in in the same window (`claude auth login`, `codex login`, or pi itself, where `/login` signs in and `/model` picks the provider), and the quick start checks again when it ends.
   You sign in there yourself; nothing is typed for you.
   An installer and a sign-in run on the console's other screen, under a line that says what is running, so what they print leaves with them; one that fails keeps that screen until you have read why.
5. It remembers the agent and starts the CFO in the CFO home, in a native terminal of its own, and says so in one line: `CFO started as Claude Code in` the home, or `CFO already running` and where.
   In a native terminal it answers the CFO's startup dialogs whose answers are known and safe, as a goblin's spawn does: Claude Code's trust in the home, Codex's directory trust and update prompt, and Codex's hook review without trusting the hooks, which stay your decision.
   It dismisses Codex's optional Daybreak security setup offer with Escape and waits for it to disappear; account security setup stays your decision.
   It reads a dialog's focus by the mark the agent draws, Claude Code's `❯` or the plain `>` it draws in a console that does not announce Unicode.
   It types nothing at a screen it does not know, such as Claude Code's own first-run questions.
   For a dialog it has not answered, in Herdr or after those questions, it says what to choose: Yes at Claude Code's trust dialog, whose first choice, No, exits, and Continue without trusting at Codex's hook review.
6. It ends on one screen with the home and the board's link, which Ctrl+click opens: **Open the CFO terminal**, which Enter takes, or **Open the board**, which B takes, in the desktop window where `goblins-window.exe` sits beside `goblins` and in the browser otherwise.
   Esc there leaves both running and exits.

A CFO that ran in a native terminal and was closed comes back in it on its conversation, and the line reads `CFO back as Claude Code on its conversation`, its id and the terminal; the README's [Everyday commands](../README.md#everyday-commands) say when it starts a new conversation instead.
Later runs skip what is already set up: with the remembered agent ready they go straight to the last screen, and with a CFO running they start nothing.
`goblins setup` shows the choice of agent again, and `goblins --harness codex|claude|pi` names it instead of asking.
`goblins resume` restarts a running CFO in its native terminal on its conversation, for a screen that froze, and with none running there does what `goblins` does; then, as after a reboot, it brings back every goblin whose terminal ended and lists which came back and which need a hand.
A screen nobody can answer, as in a script or the install's own CI run, accepts nothing and says to run `goblins` in a terminal.

A no-mistakes older than the release `install.ps1` pins is updated to it the same way, and a newer one is kept.
The install downloads and verifies the pinned release first, then stops the no-mistakes daemon, replaces the program and starts the daemon again.
no-mistakes refuses to stop its daemon while a gate runs; the install then leaves the older no-mistakes as it is and says to rerun it once no gate runs.
The install only ever updates the no-mistakes in `%LOCALAPPDATA%\no-mistakes`: an older one that comes first on PATH from anywhere else is left alone, and the install names its path and says to run `no-mistakes update` or remove it.
So when a Code Goblins release moves the pin forward, rerunning the install is how a machine moves to the no-mistakes it names.

## Updating

The board looks for a newer release when it starts and every six hours, and brings one to you as its own item in the Command Center, **Update Code Goblins**: the version you run and the new one, what is new, what the update checks, and one **Update** button, with a slim banner at the top that points to it.
**Update** runs `goblins update` for that release out of sight, and the card shows each of the steps below as it goes, then how it ended; the board is away for a few seconds and reloads on the new version.
Only you press it, from a board of your own; `"check_for_updates": false` in the home's `config\fleet.json` turns the look off.
[Update Code Goblins](native-board.md#update-code-goblins) says all it does.

`goblins update`, run in a terminal of your own, does the same: it updates the home to the newest published release without touching your goblins or the CFO.
It runs only as the home's own `goblins` or `cfo`, and it says four steps as it goes:

1. `[1/4] Download Code Goblins <version>`: it reads the newest release from GitHub and downloads its `cfo.exe`, its desktop window `goblins-window.exe` where the release ships one, its `SHA256SUMS` and its `install.ps1` into the home's `state\update`.
2. `[2/4] Check the download`: it keeps a program only when it matches the release's `SHA256SUMS`, and, for a release whose `install.ps1` names a publisher, only when Windows reports it validly signed by that publisher, the same checks the one-line install makes.
   It names each program's SHA-256, and says when the release is unsigned, which every release is until Code Goblins has a signing identity.
   A program that fails a check is never run, and nothing in the home changes.
3. `[3/4] Install Code Goblins <version>`: the downloaded build installs itself with its own `update`, which keeps the build it replaces, swaps `cfo.exe` and `goblins.exe` in `bin`, restarts only the board on the address it served, and puts the previous build back when the new one does not serve.
   The desktop window follows into `bin`; an open window keeps running the earlier one until you quit it from its tray icon, and Code Goblins in the Start menu then opens the new one.
4. `[4/4] Bring the home up to date`: the new build's `install` brings the home's contract, skills and hooks up to date, where this machine's install names this home; the board, already on the new build, is not restarted again.

It ends on one line: `Updated:`, `Rolled back:` with why the new build did not serve, or `Failed:` with what stopped it before anything changed.
When the new build serves but its install cannot refresh the home, or the machine's installed home cannot be read, it keeps that build, ends on `Updated:` saying what remains to do, and exits 6; run `goblins install` to finish.
Updating another home deliberately skips that refresh and exits 0.
The download is removed afterwards.
`goblins update --check` says how your build stands against the newest release and what is new in it, and changes nothing.
A build made from a clone has no release version, so it says to run `git pull` and then `.\install.cmd -Dev` in the clone instead.
The update is yours alone: it refuses to run under the CFO, in a goblin's or a gate's terminal, under an agent harness, and where its parents cannot be followed to the desktop, as in Git Bash; run it in PowerShell or cmd.
Rerunning the install still updates the tools it pins, such as no-mistakes, which an update leaves as they are.

## On a fresh PC

A release says at the top of its notes whether its programs are code-signed, and by whom.
Until Code Goblins has a signing identity they are not: such a release says that it is unsigned, lists the SHA-256 of each file, and its `install.ps1` names no publisher and checks the sums alone.
A signed release's `install.ps1` names its publisher, and refuses a download that publisher did not sign.
Windows knows an unsigned program only as an unknown program from an unknown publisher.
Each program's file properties (right-click, Properties, Details) name the product Code Goblins and its version, and none asks for administrator rights.

- **The one-line install** runs nothing unless the downloaded `cfo.exe` matches the release's `SHA256SUMS`.
  Windows PowerShell does not mark that download as coming from the internet, so SmartScreen does not prompt.
- **`CodeGoblinsSetup.exe`** is saved with a browser, which marks it, so while it is unsigned SmartScreen stops it once with "Windows protected your PC" and an Unknown publisher.
  Check it first: `(Get-FileHash .\CodeGoblinsSetup.exe -Algorithm SHA256).Hash` must equal the first field of the release's `SHA256SUMS` line for it, in any letter case.
  Then **More info**, **Run anyway** runs it, and what it installs is checked against the same `SHA256SUMS` by the install script.
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

It needs Go and Node.js: `winget install -e --id GoLang.Go` and `winget install -e --id OpenJS.NodeJS.LTS`.
A source build runs `npm ci` and `npm run build` in `frontend` before `go build`, because `cfo.exe` embeds the board they build, and `-Dev` does both for you.
`install.cmd` runs `install.ps1` with PowerShell's execution policy bypassed, so it works whatever execution policy is set locally.
An execution policy set by Group Policy still applies, and can refuse it.
Type it in full: in PowerShell, `.\install -Dev` runs `install.ps1` itself, which the default execution policy refuses.

`-Dev` does everything the one-line install does, with the clone in place of the download:

- It builds the board in the clone, then `cfo.exe` and the desktop window, `goblins-window.exe`, into a temporary folder of its own, runs that build's `cfo install`, which picks the home as the one-line install does, and removes the folder.
  Its steps are `[1/3] Build Code Goblins from this clone`, then installing and opening as the one-line install's.
  The clone keeps only what git ignores: the board, which `cfo.exe` embeds from there, and the frontend's `node_modules`.
  A copy in the home still running, such as a supervisor, a terminal's host or an open window, cannot be overwritten, so it moves aside to a `cfo.exe.<id>.old`, `goblins.exe.<id>.old` or `goblins-window.exe.<id>.old` of its own and is removed once nothing runs it, on this run or a later one.
- It says that the three programs are unsigned: they were built on this PC, and Windows runs a program built here without asking.
  A copy taken to another PC is unsigned there too, and [On a fresh PC](#on-a-fresh-pc) says what Windows shows for one.
- The home's `bin` goes on your PATH; open a new terminal to use it, since `install.cmd` runs in a PowerShell of its own.
  While `CFO_HOME` names another folder that holds a fleet's state, such as a clone an older build made its home, it keeps that home, as [the table above](#which-home-it-installs) says; `cfo home move` brings that fleet into the per-user home whenever you choose.
- It installs this repository's skills the same way, once in `~\.agents\skills` with a junction from Claude Code's skills folder; [load-map.md](load-map.md) shows where each harness looks for skills.

Rerun it after you pull, to rebuild.

## Where your data lives

The fleet keeps everything it writes in the CFO home, `%LOCALAPPDATA%\CodeGoblins`, for every install, `-Dev` included.
The home is outside every repository: goblins work in git worktrees of your checkouts, kept in the home's `worktrees` folder, so a checkout gains no folder and no file, and each goblin's temporary files go to the home's `scratch` folder and leave with the task.
The janitor keeps the home small, and the board and `cfo runtime` show what it holds; [AGENTS.md](../AGENTS.md#the-cfo-home) describes each folder.
It all stays on your machine: Code Goblins needs no backup repository, account or service for it.
Backing the home up, for example its `data` folder to a private git repository, is only your own choice.
`goblins uninstall` removes the hooks, the board's native hooks, the environment and the Start-menu shortcut the install set, and the desktop window's Start at login entry where it starts a program in that home, and keeps the home folder, with its state and data, until you delete it.

## Your projects folder

The app's first-run page offers the folder that holds your checkouts, wherever you keep them; the install itself asks nothing.
It is recorded on your machine as `CFO_PROJECTS_ROOT`, beside `CFO_HOME`, and never in this repository, so every adopter's layout stays their own.
With it set, `--project` takes a bare name as well as a path: `--project my-project` is `<dir>\my-project`, matched without regard to case, and the fleet works in that one checkout instead of cloning a second copy.
It is optional: without it every command still takes a path, and `goblins doctor` tells you it is unset.
To record or change it from a terminal, run `goblins install --projects-root <dir>`; a rerun of either install keeps the folder already recorded.
