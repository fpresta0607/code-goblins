# Code Goblins on Linux: inventory and plan

This is the inventory issue #2366 asks for before the port starts.
It lists every part of Code Goblins written only for Windows, what replaces it on Linux, and what Linux does not need.
It ends with the pull requests that bring the port in, one layer each, and what macOS takes from it.
Windows behaviour does not change in any of them.

The numbers were measured on main at 3bb6a058e on 2026-11-11 by reading the code, as [How it was measured](#how-it-was-measured) says.
The compile list below is a static reading of what a Linux build rejected before layer 1, which makes every package build on Linux.
Names follow the rename in #2227: the command is `goblins`, built from `cmd/goblins`.

## Where the code stands

- 2,142 Go files, of which 493 are Windows-only by file name: 128 program files and 365 tests.
- 230 files import `golang.org/x/sys/windows`.
  18 of them, 11 program files and 7 tests, carry no build constraint, so a Linux build reads them and fails.
- 58 files are built only off Windows (`//go:build !windows`).
  None is written for Linux in particular, and most of them return an error or do nothing.
- No CI job builds the command or runs a Go test on Linux.
  The six Ubuntu jobs are bookkeeping: draft state, the summary that requires every job, and the record that lets a push reuse a passing pull request run, which runs `tools/pushreuse`, the only Go that runs on Linux in CI today.
  A release builds `windows/amd64` only.
- The dependencies are pure Go and build on Linux, SQLite included (`modernc.org/sqlite`), with one exception: Wails v3, which the desktop window and the setup window use, draws with WebView2 on Windows and with WebKitGTK through cgo on Linux.
  Dictation needs cgo or a new dependency as well: on Windows it loads the speech engine's library with `LoadLibraryEx` and no cgo, but on Linux Go loads a shared library only through cgo or a pure-Go loader such as purego, which layer 9 chooses between.

### What a Linux build rejected before layer 1

About 101 program files in 23 packages, and about 80 test files, call something that exists only on Windows.
Without cgo the `main.go` of the setup window and of the desktop window fail as well, since Wails builds on Linux only through cgo.
Two packages carry most of it:

| Package | Program files in other packages that use a name it defines only on Windows |
| --- | --- |
| `internal/host`, the native terminal's host | 43 |
| `internal/proc`, process identity and enumeration | 36 |
| `internal/herdr` | 5 |
| `internal/janitor` | 4 |
| `internal/lifecycle` | 3 |
| `internal/supervisor`, `internal/browserlogin` | 2 each |
| `internal/verify`, `internal/cirunner`, `internal/update`, `internal/fsx`, `internal/lock`, `internal/watch` | 1 each |

About 69 files use one of the first two, so porting `internal/proc` and `internal/host` removes most of the list, and they come early.
The five files that use `internal/herdr` need no port of it: they lose that use when #2370 removes Herdr's code (see [Herdr](#herdr)).
Inside their own packages, about 13 supervisor files call its Windows-only functions, all but one of them the pipe calls (`sendPipeRequest`, `askPipe`, `serveRunRequests`, `Reported`) and the last one the call that hides a console (`hideConsoleOnDesktop`), and the lock calls its Windows-only process checks and waiter line; the rest are single names such as `cirunner.Ask`, `update.Hold` and `writeSynced`, `fsx.RemoveFileWhen` and `watch.NewDirWaiter`.
The untagged files that import `golang.org/x/sys/windows` are the desktop window's six, `cmd/goblins/attach.go` and `update.go`, `internal/doctor/doctor.go` and `harness_versions.go`, and `internal/spawn/native_launch.go`; `cmd/goblins/banner.go` and `launcher.go`, and `internal/reap/inventory.go`, use `syscall` names that exist only on Windows.

### Off Windows already

These `!windows` files already do the Linux job and need no new work of their own:

- `internal/disk` reads free space with `statfs`.
- `internal/gatehome` links with `os.Symlink`, though its link check (`sameFolder`), which builds everywhere, still folds case (see [Locks, files and paths](#locks-files-and-paths)).
- `internal/fsx` opens, renames, resolves and removes with the plain calls, everything but `RemoveFileWhen`, though `fsx.SamePath`, which builds everywhere, still folds case.
- `internal/auth` keeps each secret in its own file with mode 0600 when there is no Credential Manager.
- `internal/pipeline` checks that an agent's process is alive with `kill(pid, 0)`.
- `internal/verify` keeps its admission cache under `~/.cache`.
- The console checks (`own_console`, the start hook's code page) do nothing, which is right on Linux.

## Area by area

Each area names what it does on Windows, what does it on Linux, and the layer that brings it (see [Pull requests](#pull-requests-one-layer-each)).

### The native terminal

- **Windows:** `internal/conpty` runs each goblin's harness in a pseudo console (`CreatePseudoConsole`), started suspended inside a job object, with the console host the release ships (`conpty.dll`, `OpenConsole.exe`) and the system's `conhost.exe` behind it.
  An input waker works around a console host bug that leaves typed keys unread, and reads the screen back with `ReadConsoleOutputCharacterW`.
  `internal/host` is the detached process that owns that console and serves viewers.
  It starts outside its launcher's job, so it outlives whatever launched it, or inside it, marked `Contained`, where the job forbids that; the launcher starts the supervisor the same way.
  It keeps the terminal and everything it starts off the performance cores left to the Overlord's own apps (`--leave-cores-for-apps`), with the mask of threads `internal/processor` makes from each core's efficiency class.
  `goblins attach` puts the console in virtual terminal input mode and sends win32-input-mode.
  The onboarding menu reads keys with `ReadConsoleInputW`.
- **Linux:** a pseudo terminal from `openpty`, sized with `TIOCSWINSZ`, the harness in its own session (`setsid`) and process group.
  A session or process group stays in the cgroup it started in, and a systemd unit that stops ends everything in its cgroup, so each host, and the supervisor the launcher starts, starts in a transient user scope of its own (`systemd-run --user --scope`).
  A crash, restart or update of the supervisor then ends no host.
  Where no user manager answers, each starts where it is, a host marked `Contained`, as on Windows where the job forbids breaking away.
  The screen comes from the host's own emulator, which `internal/host` already keeps for its history (`internal/vtscreen`), so nothing reads it back from a console.
  The terminal's process group keeps off the same cores through `sched_setaffinity`, set on its first process and inherited by everything it starts, and `internal/processor` reads the cores' kinds and threads from `/sys`.
  `attach` and the menu use the terminal's raw mode and `SIGWINCH`.
- **Not needed:** the input waker, the shipped console host, conhost selection, power throttling, hidden console windows.
- **Layer:** 5.

### How processes reach each other

The host's protocol (a token handshake, then typed frames) is the same on every system; only what carries it is Windows-only.

- **Windows:** named pipes, each limited to the current user by its security descriptor, and each side checks the other's process id:
  - the terminal host, one pipe per terminal, which `spawn`, `send`, `peek`, `attach` and the supervisor dial;
  - the supervisor's run requests, which questions, reviews, credentials, helpers and AFK send through `sendPipeRequest` and `askPipe`;
  - the CEO's primary transport, a file opened so no one else can write it;
  - Herdr's pipe, for a fleet that runs Herdr panes;
  - the desktop CI runner's pipe.
- **Linux:** a Unix domain socket, with `SO_PEERCRED` giving each side the other's process id.
  The sockets sit in a folder only the user can open (0700), named by a short hash of the home, under `/run/user/<uid>` when that folder exists and is the user's own, or else under `/tmp`.
  The folder is chosen by the user's id, not by `$XDG_RUNTIME_DIR`, so a command run from cron, `su -` or `sudo -iu`, which has no such variable, still finds the hosts the user unit started.
  A host such a command launches is given `XDG_RUNTIME_DIR=/run/user/<uid>` for `systemd-run`, so it still gets a scope of its own.
  A folder under `/tmp` is used only once it is checked to be the user's own and 0700.
  Install turns on lingering (`loginctl enable-linger`), so the user manager and `/run/user/<uid>`, and the sockets in it, outlive the user's last logout.
  A socket's path must fit `sun_path`, 108 bytes on Linux, which a path in a long home or a test's temporary folder can exceed, so the sockets are not in the home's `state` folder.
  The primary transport is an open file held with `flock`.
- **Not needed:** Herdr's pipe.
- **Layer:** 4.

### Process trees and job objects

- **Windows:** `internal/execx` starts a child suspended, puts it in a job object that ends everything in it when closed, and resumes it.
  The gate's executor (`internal/verify`) runs each command in a named job, stops it with Ctrl+Break and finds a run's processes again after a restart by the job's name.
  `internal/lifecycle` and `internal/proc` adopt the jobs of processes the fleet did not start, through the system's handle list.
- **Linux:** every process of a tree inherits the tree's mark in its environment (`GOBLINS_TREE`), the gate's command runs in a process group of its own, and a tree is its marked processes, its group and everything they started, by their parent chain, as on macOS.
  Each member is ended by its ID through a `pidfd` once its start is checked, so a reused ID is never reached.
  `PR_SET_CHILD_SUBREAPER` is not used: it would hand an orphan back to this process, which would then have to collect it, without saying which tree it left, and after a restart only the mark finds a run's processes.
  A tree's memory and processor use is the sum over its members in `/proc`.
  The gate's run is found again by the mark it started under, its job's name; Ctrl+Break becomes `SIGINT` to the group, and the end after the grace asks each member to end with `SIGTERM` for a second, then ends it with `SIGKILL`.
  Process groups work the same on macOS, which is why they come before cgroups; a cgroup per tree stays an option if the memory floor needs a hard limit.
- **Not needed:** starting suspended and adopting another process's job; the host and the supervisor, which break away from their launcher's job on Windows, take a scope of their own on Linux instead (see [The native terminal](#the-native-terminal)).
- **Layer:** 3.

### Process identity

- **Windows:** `internal/proc` lists processes with their start times (`NtQuerySystemInformation`), reads another process's command line, folder and environment from its memory, finds the process at the other end of a local TCP connection, and ends a process only after checking its start time and image name on one handle.
  The lock, `supervise`, the teardown check, the janitor, the fleet tree, the monitor and the services dashboard all read processes through it.
  It also finds console windows and closes them.
- **Linux:** `/proc/<pid>/stat` for the parent and start time, `cmdline`, `cwd`, `environ` and `exe` for the rest, `/proc/net/tcp` and `tcp6` matched to socket inodes for the TCP peer, and `pidfd_open` so a process is ended only if it is still the one that was checked.
  Uptime comes from `/proc/uptime`.
  A start is the machine's start in `/proc/stat` plus the ticks `/proc/<pid>/stat` counts from it.
  The kernel works the machine's start out from the clock and cuts it to the second, so any step of the clock that carries it across a second, however small, moves the start of every process read after it.
  A check by start, as the one before a process is ended, therefore only ever refuses after a step.
  The lock does not rest on a start: its record and its waiters' tickets name a process by its instance, the boot ID in `/proc/sys/kernel/random/boot_id` and the raw ticks, compared exactly, with no clock in it.
  A record made from a start alone proves no process on Linux.
  Four records are still made from a start alone, and each carries the boot ID and the ticks (`lock.Info.Instance`) when its layer lands.
  The primary registration in `internal/supervisor/ceo.go`, which is the `lock.Info` that `Register` writes and the comparison of starts beside it: layer 4, with the primary transport.
  The terminal binding in `internal/supervisor/terminal.go`, which is the `lock.Info` of a `terminalBinding`: layer 5, with the native terminal.
  The watcher's handover request in `internal/watch/handover.go`, which is `handoverRequest` and `liveRequester`: layer 6, with watching.
  The updater's wait for the supervisor to exit in `cmd/goblins/update.go`, which is `serveProcess` and `processIs`: layer 8, with the updater.
  A fifth, the gate's custody record of the process that controls a run (`currentController` in `internal/verify/recover.go`), carries its instance from layer 3 part 2, with the gate's executor: without it, the next admission read a live run's controller as gone and ended the run.
  A process that has exited and only waits for its parent to collect it reads as gone.
- **Not needed:** windows, consoles and integrity levels.
- **Layer:** 3, and 6 for the TCP peer, whose one caller is the browser sign-in.

### Locks, files and paths

- **Windows:** the lock keeps a first-come line of waiters with overlapped `LockFileEx` and `CancelIoEx`; the gate's idle lock and the update's hold keep a file open so no one can replace it; `ReplaceFileW` and `MoveFileEx` with write-through replace files; a file another process holds counts as busy.
- **Linux:** `flock`, with the line of waiters kept as tickets in the lock's `.waiters` file so the first-come order survives: `flock` is held only while a ticket is written, read or struck, since it goes to whichever asker runs first; `rename` followed by `fsync` of the file and its folder; an open file is never busy, since unlinking it succeeds.
- **Must change, not only port:** code that builds on every system compares paths without regard to case, as Windows does.
  `fsx.SamePath` folds case for its 24 callers in program files, and about 50 more program files compare paths with `strings.EqualFold`, among them `internal/gatehome`, `internal/home`, `internal/harnessmap`, `internal/pipeline`, `internal/update`, `internal/uninstall`, `internal/codexguard`, `internal/fleettree`, `internal/pathguard` and `cmd/goblins`.
  11 more lower a whole path with `strings.ToLower` to compare it or use it as a key: `cmd/goblins/gate_run_end.go`, `internal/lock/lock.go`, `internal/lifecycle/processes.go`, `internal/supervisor/pages.go`, `internal/janitor/worktrees.go`, `internal/watch/janitor.go`, `internal/gatetest/select.go` and `match.go`, `internal/cleanup/outcome.go`, `internal/doctor/fix.go` and `internal/runtime/owner.go`.
  Some of the `strings.EqualFold` files do the same beside it, among them `internal/monitor/polls.go`, `internal/janitor/temp.go`, `internal/homemove/homemove.go` and `internal/supervisor/runs.go`, and `internal/supervisor/kept_reads.go` keys a folder's file names by their lowercase form.
  Some of the same code also takes `\` as the path separator, which a Linux path never holds.
  `internal/runtime/owner.go` rewrites `/` as `\` before it compares, `internal/doctor/fix.go` and `internal/homemove/homemove.go` end a folder prefix with `\`, and `internal/fleettree/reader.go` looks for `\` before a task's output file in a command line.
  None of these fails a Linux build, so the compiler does not find them; on Linux `internal/supervisor/pages.go`, for one, would make `Report.html` and `report.html` one page, and `internal/runtime/owner.go` would match a worktree only by its exact folder, never a folder below it.
  On Linux two paths that differ only in case are two folders, so all of them compare exactly there.
  `internal/connections` folds case only in its Windows file, so its Linux file compares exactly from the start.
  About 80 program files spell a program as `name.exe`; each gets the system's own name.
- **Layer:** 3 for the locks and files, 4 for the path comparison.

### The home and the user's folders

- **Windows:** code that builds on every system finds the user's folders through Windows variables.
  `home.DefaultRoot` puts the home under `%LOCALAPPDATA%` and fails when it is unset, so on Linux every command fails unless `GOBLINS_HOME` names a home.
  `goblins install` from a clone, `goblins update` when it reads the home of a running server, and the uninstaller each find that folder through `LOCALAPPDATA` themselves.
  Install narrows the home and the Dev Drive folder (`internal/access`) so that only the user, SYSTEM and Administrators can open them.
  Off Windows `internal/access` narrows nothing and reports every folder narrowed, while install makes the home's folders with mode 0755, so on Linux install would report a home other accounts can read as narrowed.
  `internal/pathguard` guards the Credential Manager's folders under `APPDATA` and `LOCALAPPDATA` beside `~/.ssh` and the file store, and reads a path that starts with a drive letter folder such as `/c/`, or with `/tmp`, as Git Bash would.
- **Linux:** the home defaults to `$XDG_DATA_HOME/code-goblins`, or `~/.local/share/code-goblins` when that is unset, and install, update and the uninstaller follow the same rule.
  The home and its folders are made 0700, and `access.IsNarrow` reads the mode, so install reports what is true.
  `internal/pathguard` guards `~/.ssh` and the file store, and reads a path as it is written.
- **Layer:** 3 for the default home and its access, 4 for the guarded folders, 8 for install, update and the uninstaller.

### Watching and machine readings

- **Windows:** `internal/watch` waits on folders with `ReadDirectoryChangesW`.
  The supervisor reads free memory and the commit charge (`GlobalMemoryStatusEx`, `NtQuerySystemInformation`), each processor's use, GPU counters and the kernel's pool tags, the sign-in time from the logon session, and a file's creation time.
  `internal/priority` raises the supervisor above the work.
- **Linux:** `inotify`; `/proc/meminfo` (`MemAvailable`, `Committed_AS`, `CommitLimit`), `/proc/stat`, and each process's `VmRSS` and `VmSwap`; the creation time from `statx`; the sign-in time from the login session; `setpriority` and `ioprio_set`.
- **Not needed:** pool tags, which watch a Windows kernel leak.
- **Left out at first:** GPU readings, which have no vendor-neutral source on Linux.
- **Layer:** 6.

### The credential store

- **Windows:** Credential Manager, which encrypts each secret under the user's sign-in.
- **Linux:** the file store that is already the fallback: one file per secret, mode 0600, in a folder only the user opens.
  The Secret Service (through D-Bus) can follow, but a server without a desktop session has none, so the file store stays the default.
- **Layer:** none; it works today.

### Signing in through a browser

- **Windows:** `internal/browserlogin` finds Chrome through the registry, runs it in a job object and talks to it over DevTools pipes.
- **Linux:** Chrome or Chromium found on `PATH` (`google-chrome`, `chromium`), run in its own process group; the DevTools connection is the same.
- **Layer:** 6.

### Installer, updater and uninstaller

- **Windows:** `install.ps1` (and `install.cmd` for a clone) downloads the release's programs, checks them against `SHA256SUMS` and, when the release names a publisher, their Authenticode signature; installs git and gh with winget; writes the user's `PATH` to the registry; makes Start menu and desktop shortcuts; registers the app in Installed apps; starts it at sign-in from the registry's `Run` key.
  `CodeGoblinsSetup.exe` is a window that runs the same script.
  `goblins update` replaces the programs the home runs while they are held, and checks the signature with PowerShell.
  The uninstaller removes the registry entries, shortcuts, scheduled tasks and the Dev Drive, elevating where it must.
- **Linux:** a `tar.gz` per architecture (`linux/amd64`, `linux/arm64`) beside an `install.sh` that downloads it, checks it against the same `SHA256SUMS`, and puts the programs in `~/.local/bin` and the home in `$XDG_DATA_HOME/code-goblins`.
  git and gh come from the system's package manager, which the script names rather than runs.
  `PATH` is added in the shell's profile when `~/.local/bin` is not on it already.
  A `.desktop` entry opens the board, and start at sign-in is a systemd user unit, which also runs on a server with no desktop.
  Install turns on lingering (`loginctl enable-linger`), so the user manager, and the unit with it, starts at boot and stays up after the last logout, which a server reached over SSH needs.
  `goblins update` replaces a program by renaming the new file over it, which Linux allows while it runs.
  The uninstaller removes the programs, the `.desktop` entry and the unit, and turns lingering off again if install turned it on.
- **Not needed:** the setup window, the registry, Installed apps, shortcuts, elevation, Authenticode (the checksums remain; a signature for the archive can follow).
- **Layer:** 8.

### Harness hooks and PowerShell

- **Windows:** the goblins' hooks run a second copy of the command built as a windowed program, so no console flashes for each hook.
  Claude Code's native hooks run a PowerShell helper script; Codex's run `goblins native-hook codex` directly, and fall back to the helper only when a path would need quoting.
  The commands the Overlord runs from a card (credentials, connections, releases, remote access, a wake, a code run, Dev Drive) are written for PowerShell.
  The run requests find Windows PowerShell under `SystemRoot` and bash beside Git for Windows (`runShellPath`), and a message to a goblin that starts with `!` runs in that Windows PowerShell.
  A card opens a folder or a path in File Explorer, found under `SystemRoot`, and only for a path on a drive.
  Other programs start Windows PowerShell themselves: the harness version probe, the quickstart, the machine readings of `goblins runtime`, the reaper's process list, the release signature check, the Defender check, the uninstaller's Dev Drive step and the CI runner.
  18 PowerShell scripts install, release and prove the install on CI.
- **Linux:** the hooks call `goblins` itself, so there is one program and no helper script.
  A card's command is written for bash, which the run requests already accept beside PowerShell.
  bash and `pwsh` come from `PATH`, a message that starts with `!` runs in bash, and a card opens a folder or a path with `xdg-open`.
  The version probe runs the harness directly, the machine readings and the process list read `/proc`, and the rest sit in areas Linux does not need.
  The installer's proofs get shell counterparts with the installer.
- **Layer:** 7 for the hooks and the cards, and each other caller's own layer.

### The janitor's drive handling

- **Windows:** Dev Drive (`internal/devdrive`: ReFS state, the VHD behind it, and the scheduled task that mounts it at boot) and the Defender checks (`internal/defender`).
- **Linux:** nothing replaces them; the janitor's folder and cache work already uses portable calls.
- **Not needed:** both.

### The desktop window

- **Windows:** `cmd/goblins-window` is a Wails v3 window on WebView2, with the registry for start at sign-in and the notification identity, Win32 drawing for its notices and a message box.
  After an update the supervisor opens the new window through File Explorer, found under `SystemRoot`.
- **Linux, first:** the board opens in the user's browser, which it already serves; the `.desktop` entry opens it there.
- **Linux, later:** Wails draws the same window with WebKitGTK, which needs cgo and the GTK and WebKitGTK development packages on the build runner.
- **Layer:** 8 for the browser, a later pull request for the window.

### Dictation and the clipboard

- **Windows:** dictation loads the sherpa speech engine's Windows x64 library with `LoadLibraryEx` and calls it without cgo; the clipboard is the Win32 clipboard.
- **Linux:** the sherpa engine's Linux library, with a pin of its own beside the Windows one in `config/voice.json`, loaded through cgo or a pure-Go loader such as purego, which layer 9 chooses; `wl-copy` or `xclip` for the clipboard.
- **Layer:** 9 for dictation, 6 for the clipboard.

### The desktop CI runner

- **Windows:** `cmd/cg-ci-runner` is a Windows service that runs private repositories' CI in a Hyper-V virtual machine, reached by an access-limited pipe, so that CI does not use paid GitHub runners.
- **Linux:** a systemd system unit that runs the jobs in a KVM machine (`internal/cirunner` already reads qcow2 disks), reached by an access-limited Unix socket instead of the pipe.
  Without it, a Linux machine would send private repositories' CI back to paid GitHub runners.
- **Layer:** 10.

### Herdr

- **Windows:** `internal/herdr`, a client for Herdr's panes over its own pipe, which 87 files import.
  The fleet no longer runs Herdr panes: every goblin and the CEO run in native terminals, and the install neither installs nor checks Herdr.
- **Linux:** nothing; its code leaves the repository with #2370.
  Until then, its Windows-only names get the Linux file every name gets in layer 1, and nothing more.
- **Not needed:** all of it.

### Restart, folder picker and other edges

- **Windows:** the supervisor can restart the PC to free kernel pool memory (`InitiateShutdownW`), and the first-run page picks the projects folder with the system's folder dialog.
- **Linux:** the projects folder is typed or chosen in the board.
- **Not needed:** the restart, which answers a Windows kernel leak.

## Not needed on Linux

- The console host, its input waker and every hidden-window flag.
- The windowed hook program.
- Job object adoption and suspended starts.
- Pool tags and the restart that clears them.
- Dev Drive and Defender.
- The registry, Installed apps, Start menu shortcuts and elevation.
- Long path prefixes, 8.3 short names and file sharing modes.
- The setup window.
- Herdr, whose code #2370 removes.

## Left out on Linux at first

Nothing is dropped in silence: each of these but the last says what it lacks when used, until its own pull request lands, and the last is named here, since nothing can say it when it happens.

- GPU readings: no vendor-neutral source.
- The desktop window: the board opens in the browser instead.
- A process that clears or replaces its environment, leaves its tree's process group and leaves the parent chain, because a process between it and the fleet has exited, is not found: it outlives the run, as on macOS.

## Pull requests, one layer each

Each layer is one pull request on the latest main, layer 3 three of them, keeps the Windows suite green, and is proved by CI on its Ubuntu runner.
A layer's Linux code sits beside the Windows code under a build tag.

1. **This inventory, then the build compiling on Linux.**
   Every Windows-only name the rest of the code reaches gets a Linux file beside it that, until its own layer, refuses with an error wrapping `errors.ErrUnsupported` that names what is missing, or answers as the Windows code does when it cannot read, such as a process not found.
   Where Linux never needs the work, the answer is final: no Codex desktop app, which is a Windows package, and no console window to hide.
   Every untagged file that reaches Windows calls moves its Windows part into a `_windows.go` file, and the types, constants and logic that hold on any system move out of the Windows files into untagged ones, so none is written twice.
   The setup window builds only on Windows, and the desktop window and the CI runner's service do too until their own layers.
   A Linux job in CI builds every package (`GOOS=linux go build ./...`), so a change that breaks the Linux build fails there.
2. **Unit tests on Linux.**
   Windows-only tests take the `_windows_test.go` name, so every package's tests compile and vet on Linux, and fixtures that named Windows paths build one for the system the test runs on.
   The Linux job runs `go vet` and `go test` on every package outside a list the job keeps of the packages whose tests wait on product code a later layer ports, such as the lock's line of waiters, process readings, file replacement, path comparison and the native terminal.
3. **Processes, trees, locks and files.**
   `internal/proc` on `/proc`, `internal/execx` and the gate's executor on process groups, the lock on `flock`, the file replacements, and the default home under `$XDG_DATA_HOME`, kept to the user with mode 0700.
   It comes in three pull requests: processes and the lock, then trees and the gate's executor, then the files and the home.
4. **Transport.** Unix sockets for the host, the run requests and the primary transport, in the user's 0700 folder under `/run/user/<uid>` (or `/tmp`), chosen by the user's id and named by a short hash of the home so each path fits `sun_path`, exact path comparison in `fsx.SamePath`, the `strings.EqualFold` calls and the paths lowered with `strings.ToLower`, the code that takes `\` as the separator, and the folders `internal/pathguard` guards.
   Until layer 8 turns lingering on, the socket folder lasts only while the user is signed in.
   The primary registration names its process by instance, as the lock's record does (see [Process identity](#process-identity)).
5. **The native terminal.** `internal/host` on a pseudo terminal, each host and the supervisor the launcher starts in a transient user scope of its own, the terminal kept off the cores left to the Overlord's own apps with `sched_setaffinity` and the core kinds read from `/sys`, `attach` and the onboarding menu in raw mode, and `spawn` launching on Linux.
   The terminal binding names its process by instance.
6. **Watching and readings.** `inotify`, the memory and processor readings, priorities, the janitor's processes, the browser sign-in and the clipboard.
   The watcher's handover request names its requester by instance.
7. **End to end.** The hooks without PowerShell, card commands and `!` messages in bash, folders opened with `xdg-open`, and a goblin run on Linux in CI under Claude Code, Codex and pi: spawn, send, peek, wake, pause, resume, gate and cleanup.
8. **Installer and updater.** The release's Linux archives and `install.sh`, update and uninstall, the `.desktop` entry, the user unit and lingering, the board opening in the browser, and the README's download control and the install guide offering Linux beside Windows.
   The updater's wait for the supervisor to exit names the supervisor by instance.
9. **Dictation.** The speech engine's Linux library, pinned in `config/voice.json` beside the Windows one, and loaded through cgo or a pure-Go loader such as purego, whichever this layer chooses.
   If it takes cgo, the release's Linux build from layer 8 turns cgo on with it.
10. **The desktop CI runner.** The runner as a systemd system unit running private repositories' CI in a KVM machine.

Layers 3 to 5 hold most of the work; layer 7 is where a goblin first works on Linux, and layer 8 is where someone else can install it.

## macOS

Issue #2535 brings Code Goblins to the Mac: one signed download, its own desktop app, and a goblin that works there under each harness.
It comes ahead of the Linux installer (layer 8), and the Linux layers stay, since macOS takes most of them.
Mac proof is CI on a macOS runner, as Linux proof is the `linux` job.

### What macOS shares with Linux

macOS takes the pseudo terminal, Unix sockets, process groups, signals and `flock` from the Linux layers.
Code that holds on both is written once, in `_unix.go` files, so neither system gets a copy.
A durable write is shared too: `fsync` alone does not reach the disk on macOS, but Go's `File.Sync` already issues `F_FULLFSYNC` there, so it costs macOS nothing.
It differs where Linux reads `/proc` and `/sys`, and where the Linux layers call what only Linux has:

- Process identity comes from `sysctl` (`kern.proc` for the parent, name and start, `kern.procargs2` for the arguments and environment) and from `libproc` for the working folder, the image and each process's memory.
- A process is named by the boot session's UUID (`kern.bootsessionuuid`) and its start in the ticks of the machine's absolute clock (`ri_proc_start_abstime`, from `libproc`'s `proc_pid_rusage`), which counts from the machine's start and is never stepped, so no wall clock is in it, as none is in the Linux instance.
  The kernel's own unique ID of a process (`p_uniqueid`) would serve too, but the public SDK does not declare the flavors that read it.
- No `pidfd_open`: a process is ended by its ID after its identity is checked, so a few microseconds stay between the check and the end in which the ID could pass to another program.
  A child the fleet started cannot lose its ID that way, since its parent has not collected it.
- No `PR_SET_CHILD_SUBREAPER`: a child whose parent exits passes to launchd, so its parent chain no longer leads back to the fleet.
  `internal/execx` and the gate's executor mark each tree they start with an environment marker, and a tree is found by its parent chain and by that marker, read through `kern.procargs2`, which M1 ports.
  Linux finds a tree the same way, so the code is written once.
- No `sched_setaffinity`: the terminal is kept off the performance cores by its priority, and the cores' kinds come from `sysctl hw.perflevel*`.
  Background priority (`PRIO_DARWIN_BG`) runs it on the efficiency cores of Apple silicon, but it also gives the lowest scheduling priority and throttles disk and network, which the Windows thread mask and Linux affinity do not.
  Spawn always passes `--leave-cores-for-apps`, so every goblin's work would be throttled.
  Layer 5 measures background priority against the utility class (`QOS_CLASS_UTILITY`) on the macOS runner, chooses from those numbers, and states the choice and its cost in its pull request.
- No systemd: each host starts in a session of its own, and the supervisor runs as a launchd agent with `AbandonProcessGroup`, so an update of the supervisor ends no host.
- The socket's peer comes from `LOCAL_PEERPID` instead of `SO_PEERCRED`, and the sockets take the user's 0700 folder under `/tmp`, since the 104-byte `sun_path` leaves no room under `$TMPDIR`.
- Folder watching uses `kqueue`, memory and processor readings use the Mach host calls, the creation time comes from `stat`'s birth time and the disk priority from `setiopolicy_np`.
- Its default file system ignores case and Unicode normalization while it keeps both, as Windows ignores case, so layer 4's exact comparison is wrong there: a path compares as its volume does, read with `pathconf(_PC_CASE_SENSITIVE)`.
- The home follows the Linux rule, `$XDG_DATA_HOME/code-goblins` or `~/.local/share/code-goblins`, rather than `~/Library/Application Support`, whose space every hook and card command would have to quote; the app itself sits in `/Applications`.
- bash is 3.2 on every Mac, and zsh is the user's shell, so a card's command is written for `sh` and a `!` message runs in the user's shell.
- The credential store is the Keychain, through the Security framework, from the installer's layer on, since the Keychain trusts a signed app; until then the file store Linux uses works.
- The window is Wails on WKWebView, which Wails already draws, so the Mac gets the desktop window with its installer, not later as Linux does.

### cgo on macOS

`golang.org/x/sys` covers `sysctl`, `kqueue`, `flock`, `LOCAL_PEERPID` and the terminal's ioctls without cgo.
`libproc`, the Mach host readings, `setiopolicy_np` and the Security framework have no such call, and the desktop window needs cgo through Wails anyway.
So the macOS build turns cgo on and builds on a macOS runner, which carries Apple's compiler, and no new dependency comes in.
A pure-Go loader such as purego is the alternative; it adds a dependency and saves only the CLI's cgo.

### Layer by layer

Each remaining layer is one pull request for Linux and macOS together, proved on the Ubuntu and the macOS runners.
The sizes are added lines, measured against layer 3 part 1, which added 985 program lines, 675 of them Linux only, and 1,202 test lines.

| Layer | macOS shares with Linux | macOS needs of its own | Pull request | What macOS adds to it |
| --- | --- | --- | --- | --- |
| 1 and 2, build and unit tests | the build constraints, the `_windows_test.go` split, the EXCEPT list | a `macos` CI job that builds, vets and runs the tests of every package outside its own EXCEPT list; Windows-only test programs (`cmd`, `ping`) that are not yet per system | M1, macOS catches up, right after this plan | about 150 program lines, 100 test lines, the job (a workflow change for the CEO) |
| 3 part 1, processes and the lock | the lock's line of waiters, its instance rule, `fsx.RemoveFileWhen`, supervise's liveness check, the stand-in programs (`true`, `sleep`) | `internal/proc` on `sysctl` and `libproc`; the instance from the boot session and the process's start in absolute-clock ticks; ending a process without a `pidfd` | M1 | about 350 program lines, 300 test lines |
| 3 part 2, trees and the gate's executor | process groups, signals, Ctrl+Break as `SIGINT` then `SIGTERM` and `SIGKILL`, the environment marker on each tree `internal/execx` and the gate's executor start, and the tree found by that marker, its group and its parent chain | the marker read through `kern.procargs2`; each process's memory and processor time from `libproc`'s `proc_pid_rusage` | 3 part 2 | about 100 program lines, 80 test lines |
| 3 part 3, files and the home | `rename`, the hold on a file, the home under `$XDG_DATA_HOME`, mode 0700, `access.IsNarrow` by mode, the durable write through `File.Sync` | nothing of its own | 3 part 3 | nothing |
| 4, transport and paths | Unix sockets, the peer check, `flock` on the primary transport, `pathguard` reading a path as written | `LOCAL_PEERPID`; sockets under `/tmp`; path comparison by the volume's case and normalization | 4 | about 150 program lines, 150 test lines |
| 5, the native terminal | the pseudo terminal, raw mode, `SIGWINCH`, the host's emulator | the pty from `/dev/ptmx`, the launchd agent's `AbandonProcessGroup` in place of a systemd scope, background priority or the utility class in place of affinity, chosen by measuring both on the macOS runner, the cores' kinds from `sysctl` | 5 | about 200 program lines, 120 test lines |
| 6, watching and readings | the reader's shape, `setpriority`, Chrome over DevTools | `kqueue`; memory and processors from the Mach host calls; the birth time; `setiopolicy_np`; Chrome in `/Applications`; `pbcopy` | 6 | about 350 program lines, 200 test lines |
| 7, end to end | the hooks calling `goblins`, the run on CI under Claude Code, Codex and pi | cards in `sh`, `!` in the user's shell, folders opened with `open` | 7 | about 40 program lines, and the macOS runner in the end-to-end job |
| 8 for macOS, installer, window and updater | the release's checksums, update by rename | one universal disk image for Apple silicon and Intel holding the app with the window and the CLI, signed and notarized; start at sign-in as a launch agent; the Keychain; update and uninstall of the app | 8m, its own pull request, ahead of the Linux installer | about 600 program lines, 200 test lines, and the release job on a macOS runner |
| 8 for Linux | | | 8, after 8m | nothing |
| 9, dictation | the loader layer 9 chooses | the sherpa engine's universal library, the microphone permission, which the signed app asks for | 9 | about 80 program lines |
| 10, the desktop CI runner | | | 10, Linux only | nothing: left out on macOS (see below) |

### What part 1 of layer 3 leaves for macOS

It replaced the `!windows` stubs that `internal/proc`, `internal/lock` and `internal/supervise` built on, and its last commit brought them back for systems other than Windows and Linux, so `GOOS=darwin go build ./...` passes and those packages refuse on macOS as they did before.
The tests of `internal/lock` and `internal/supervise` do not yet compile for macOS, since their stand-in programs (`endedProgram`, `lastingProgram`) are per system: M1 makes them `_unix` and deletes the stubs as it ports.
The portable tests of `internal/proc` already compile for macOS, and its system tests are `_windows_test.go` and `_linux_test.go` files, so M1 adds its macOS tests beside them.
M1 brings, for macOS: `Lookup`, `Processes`, `Ancestry`, `StartTime`, `WorkingDirectory`, `Arguments`, `Environment`, `Identify`, `TerminateVerified`, `TerminateVerifiedIn`, `Instance` and `runningProcesses`, and the lock's `processStart` and `readInstance`.
The lock's line of waiters and supervise's `pidAlive` already hold on macOS and move to `_unix.go` names unchanged, and `fsx.RemoveFileWhen` already builds there, off Windows.
The four records the plan names as built from a start alone carry the instance on macOS in the same layers as on Linux.

### Signing and notarization

The release signs the app and the CLI with a Developer ID certificate under the hardened runtime, has Apple notarize the disk image, and staples the ticket to it.
These are the repository owner's steps: the release needs five repository secrets, which only the owner adds, and no identity, account or name goes into code, tests, documents or release notes.

- `MACOS_SIGNING_CERTIFICATE`: the Developer ID Application certificate and its key, as a base64 `.p12`.
- `MACOS_SIGNING_CERTIFICATE_PASSWORD`: the `.p12`'s password.
- `MACOS_NOTARY_KEY`: an App Store Connect API key, as the `.p8` file's text.
- `MACOS_NOTARY_KEY_ID` and `MACOS_NOTARY_ISSUER_ID`: that key's ID and issuer.

The release notes and the install guide say "Signed with an Apple Developer ID and notarized by Apple".

### Not needed on macOS

The same list as Linux, and the systemd scope, the user unit and lingering, which launchd replaces.

### Left out on macOS at first

Nothing is dropped in silence: each of these but the last says what it lacks when used, and the last is named here, since nothing can say it when it happens.

- GPU readings: macOS gives them only to root (`powermetrics`).
- The desktop CI runner: a Mac sends private repositories' CI to GitHub's runners until a runner on the Virtualization framework is planned.
- Intel Macs are built into the universal image and run in CI only while GitHub offers an Intel macOS runner.
- A process that clears or replaces its environment, leaves its tree's process group and leaves the parent chain, because a process between it and the fleet has exited, is not found: it outlives the run.

### The order

1. This plan.
2. M1: macOS catches up to layer 3 part 1, with its CI job.
3. Layers 3 part 2, 3 part 3, 4, 5, 6 and 7, each for Linux and macOS.
4. 8m: the Mac installer, window and updater, signed and notarized.
5. 8: the Linux installer.
6. 9 and 10.

## How it was measured

Every tracked Go file was classed by its file name and its `//go:build` line as Windows-only, built only off Windows, or built everywhere.
Every package and its tests were then type-checked as Linux (`GOOS=linux`, `CGO_ENABLED=0`) with the standard library's `go/types`, and each file with a type error was counted.
The type checker reports a failed import only once per package, so the untagged files that import `golang.org/x/sys/windows` were added by their imports, and a string that only mentions that path does not count as an import.
The table counts, for each package, the program files in other packages with an error that names something it defines.
It is still a static reading, not a build, which is why the first Linux build in CI is the measure that counts.
