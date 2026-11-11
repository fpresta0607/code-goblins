# Code Goblins on Linux: inventory and plan

This is the inventory issue #2366 asks for before the port starts.
It lists every part of Code Goblins written only for Windows, what replaces it on Linux, and what Linux does not need.
It ends with the pull requests that bring the port in, one layer each, and what macOS takes from it.
Windows behaviour does not change in any of them.

The numbers were measured on main at 3bb6a058e on 2026-11-11 by reading the code, as [How it was measured](#how-it-was-measured) says.
No Linux build has run yet, so the compile list below is a static reading; the first Linux CI job replaces it with the compiler's own list.
Names follow the rename in #2227: the command is `goblins`, built from `cmd/goblins`.

## Where the code stands

- 2,142 Go files, of which 492 are Windows-only by file name: 127 program files and 365 tests.
- 233 files import `golang.org/x/sys/windows`.
  19 of them, 11 program files and 8 tests, carry no build constraint, so a Linux build reads them and fails.
- 58 files are built only off Windows (`//go:build !windows`).
  None is written for Linux in particular, and most of them return an error or do nothing.
- No CI job builds the command or runs a Go test on Linux.
  The six Ubuntu jobs are bookkeeping: draft state, the summary that requires every job, and the record that lets a push reuse a passing pull request run, which runs `tools/pushreuse`, the only Go that runs on Linux in CI today.
  A release builds `windows/amd64` only.
- The dependencies are pure Go and build on Linux, SQLite included (`modernc.org/sqlite`), with one exception: the desktop window's Wails v3 draws with WebView2 on Windows and with WebKitGTK through cgo on Linux.

### What a Linux build rejects today

119 program files in 30 packages, and 178 test files, call something that exists only on Windows.
Two packages carry most of it:

| Package | Program files in other packages that use a name it defines only on Windows |
| --- | --- |
| `internal/host`, the native terminal's host | 43 |
| `internal/proc`, process identity and enumeration | 35 |
| `internal/herdr` | 5 |
| `internal/janitor` | 4 |
| `internal/lifecycle` | 3 |
| `internal/supervisor`, `internal/browserlogin` | 2 each |
| `internal/verify`, `internal/cirunner`, `internal/update`, `internal/fsx`, `internal/lock`, `internal/watch` | 1 each |

68 files use one of the first two, so porting `internal/proc` and `internal/host` removes most of the list, and they come early.
Inside their own packages, 19 supervisor files call its Windows-only functions, mostly the pipe calls (`sendPipeRequest`, `askPipe`, `serveRunRequests`, `Reported`) and the run launcher, and the lock calls its Windows-only waiter line; the rest are single names such as `cirunner.Ask`, `update.Hold` and `writeSynced`, `fsx.RemoveFileWhen` and `watch.NewDirWaiter`.
The untagged files that import `golang.org/x/sys/windows` are the desktop window's six, `cmd/goblins/attach.go` and `update.go`, `internal/doctor/doctor.go` and `harness_versions.go`, and `internal/spawn/native_launch.go`; `cmd/goblins/banner.go` and `launcher.go` use `syscall` fields that exist only on Windows.

### Off Windows already

These `!windows` files already do the Linux job and need no new work:

- `internal/disk` reads free space with `statfs`.
- `internal/gatehome` links with `os.Symlink`.
- `internal/fsx` opens, renames, resolves and removes with the plain calls, everything but `RemoveFileWhen`.
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
  `goblins attach` puts the console in virtual terminal input mode and sends win32-input-mode.
  The onboarding menu reads keys with `ReadConsoleInputW`.
- **Linux:** a pseudo terminal from `openpty`, sized with `TIOCSWINSZ`, the harness in its own session (`setsid`) and process group.
  The screen comes from the host's own emulator, which `internal/host` already keeps for its history (`internal/vtscreen`), so nothing reads it back from a console.
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
- **Linux:** a Unix domain socket under the home's `state` folder, in a folder only the user can open (0700), with `SO_PEERCRED` giving each side the other's process id.
  The primary transport is an open file held with `flock`.
- **Layer:** 4.

### Process trees and job objects

- **Windows:** `internal/execx` starts a child suspended, puts it in a job object that ends everything in it when closed, and resumes it.
  The gate's executor (`internal/verify`) runs each command in a named job, stops it with Ctrl+Break and finds a run's processes again after a restart by the job's name.
  `internal/lifecycle` and `internal/proc` adopt the jobs of processes the fleet did not start, through the system's handle list.
- **Linux:** each tree in its own process group, ended with `kill(-pgid)`, with `PR_SET_CHILD_SUBREAPER` so a child that leaves its group still comes back to its parent.
  A tree's memory and processor use is the sum over its members in `/proc`.
  The gate's run is found again by the group id it records; Ctrl+Break becomes `SIGINT`, then `SIGTERM`.
  Process groups work the same on macOS, which is why they come before cgroups; a cgroup per tree stays an option if the memory floor needs a hard limit.
- **Not needed:** starting suspended, breakaway flags, adopting another process's job.
- **Layer:** 3.

### Process identity

- **Windows:** `internal/proc` lists processes with their start times (`NtQuerySystemInformation`), reads another process's command line, folder and environment from its memory, finds the process at the other end of a local TCP connection, and ends a process only after checking its start time and image name on one handle.
  The lock, `supervise`, the teardown check, the janitor, the fleet tree, the monitor and the services dashboard all read processes through it.
  It also finds console windows and closes them.
- **Linux:** `/proc/<pid>/stat` for the parent and start time, `cmdline`, `cwd`, `environ` and `exe` for the rest, `/proc/net/tcp` and `tcp6` matched to socket inodes for the TCP peer, and `pidfd_open` so a process is ended only if it is still the one that was checked.
  Uptime comes from `/proc/uptime`.
- **Not needed:** windows, consoles and integrity levels.
- **Layer:** 3.

### Locks, files and paths

- **Windows:** the lock keeps a first-come line of waiters with overlapped `LockFileEx` and `CancelIoEx`; the gate's idle lock and the update's hold keep a file open so no one can replace it; `ReplaceFileW` and `MoveFileEx` with write-through replace files; a file another process holds counts as busy.
- **Linux:** `flock`, with the line of waiters kept in the lock's own file so the first-come order survives; `rename` followed by `fsync` of the file and its folder; an open file is never busy, since unlinking it succeeds.
- **Must change, not only port:** `internal/pathguard` and `internal/connections` compare paths without case, as Windows does.
  On Linux two paths that differ only in case are two folders, so they compare exactly there.
  About 80 program files spell a program as `name.exe`; each gets the system's own name.
- **Layer:** 3 for the locks and files, 4 for the path comparison.

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
  `goblins update` replaces a program by renaming the new file over it, which Linux allows while it runs.
  The uninstaller removes the programs, the `.desktop` entry and the unit.
- **Not needed:** the setup window, the registry, Installed apps, shortcuts, elevation, Authenticode (the checksums remain; a signature for the archive can follow).
- **Layer:** 8.

### Harness hooks and PowerShell

- **Windows:** the goblins' hooks run a second copy of the command built as a windowed program, so no console flashes for each hook.
  The native hooks for Claude Code and Codex run a PowerShell helper script.
  The commands the Overlord runs from a card (credentials, connections, releases, remote access, a wake, a code run, Dev Drive) are written for PowerShell.
  Other programs start Windows PowerShell themselves: the harness version probe, the quickstart, the machine readings of `goblins runtime`, the reaper's process list, the release signature check, the Defender check, the uninstaller's Dev Drive step and the CI runner.
  18 PowerShell scripts install, release and prove the install on CI.
- **Linux:** the hooks call `goblins` itself, so there is one program and no helper script.
  A card's command is written for bash, which the run requests already accept beside PowerShell.
  The version probe runs the harness directly, the machine readings and the process list read `/proc`, and the rest sit in areas Linux does not need.
  The installer's proofs get shell counterparts with the installer.
- **Layer:** 7 for the hooks and the cards, and each other caller's own layer.

### The janitor's drive handling

- **Windows:** Dev Drive (`internal/devdrive`: ReFS state, the VHD behind it, and the scheduled task that mounts it at boot) and the Defender checks (`internal/defender`).
- **Linux:** nothing replaces them; the janitor's folder and cache work already uses portable calls.
- **Not needed:** both.

### The desktop window

- **Windows:** `cmd/goblins-window` is a Wails v3 window on WebView2, with the registry for start at sign-in and the notification identity, Win32 drawing for its notices and a message box.
- **Linux, first:** the board opens in the user's browser, which it already serves; the `.desktop` entry opens it there.
- **Linux, later:** Wails draws the same window with WebKitGTK, which needs cgo and the GTK and WebKitGTK development packages on the build runner.
- **Layer:** 8 for the browser, a later pull request for the window.

### Dictation and the clipboard

- **Windows:** dictation loads the sherpa speech engine's Windows x64 library; the clipboard is the Win32 clipboard.
- **Linux:** the sherpa engine's Linux library, with a pin of its own beside the Windows one in `config/voice.json`; `wl-copy` or `xclip` for the clipboard.
- **Left out at first:** dictation, which says it needs the engine until its layer lands.
- **Layer:** 9 for dictation, 6 for the clipboard.

### The desktop CI runner

- **Windows:** `cmd/cg-ci-runner` is a Windows service that runs private repositories' CI in a Hyper-V virtual machine, reached by an access-limited pipe.
- **Linux:** a systemd system unit and a KVM machine (`internal/cirunner` already reads qcow2 disks).
- **Left out:** the whole runner, which serves the one Windows machine it was built for; it ports when a Linux machine is asked to run CI.

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

## Left out on Linux at first

Nothing is dropped in silence: each of these says what it lacks when used, until its own pull request lands.

- GPU readings: no vendor-neutral source.
- Dictation: needs the engine's Linux library (layer 9).
- The desktop window: the board opens in the browser instead.
- The desktop CI runner: ports when a Linux machine is asked to run CI.
- Herdr panes: the fleet runs native terminals, and Herdr's Linux socket follows only if it is asked for.

## Pull requests, one layer each

Each layer is one pull request on the latest main, keeps the Windows suite green, and is proved by CI on its Ubuntu runner.
A layer's Linux code sits beside the Windows code under a build tag.

1. **This inventory, then the build compiling on Linux.**
   Every Windows-only name gets a Linux file beside it that, until its own layer, returns an error naming what is missing, and every untagged file that reaches Windows calls moves its Windows part into a `_windows.go` file.
   The desktop window and the CI runner's service build only on Windows until their own layers.
   A Linux job in CI builds every package (`GOOS=linux go build ./...`), so a change that breaks the Linux build fails there.
2. **Unit tests on Linux.**
   Windows-only tests take the `_windows_test.go` name, and the Linux job runs `go vet` and `go test` on every package.
3. **Processes, trees, locks and files.**
   `internal/proc` on `/proc`, `internal/execx` and the gate's executor on process groups, the lock on `flock`, and the file replacements.
4. **Transport.** Unix sockets for the host, the run requests and the primary transport, and exact path comparison.
5. **The native terminal.** `internal/host` on a pseudo terminal, `attach` and the onboarding menu in raw mode, and `spawn` launching on Linux.
6. **Watching and readings.** `inotify`, the memory and processor readings, priorities, the janitor's processes, the browser sign-in and the clipboard.
7. **End to end.** The hooks without PowerShell, card commands in bash, and a goblin run on Linux in CI under Claude Code, Codex and pi: spawn, send, peek, wake, pause, resume, gate and cleanup.
8. **Installer and updater.** The release's Linux archives and `install.sh`, update and uninstall, the `.desktop` entry and the user unit, the board opening in the browser, and the README's download control and the install guide offering Linux beside Windows.
9. **Dictation.** The speech engine's Linux library.

Layers 3 to 5 hold most of the work; layer 7 is where a goblin first works on Linux, and layer 8 is where someone else can install it.

## macOS after Linux

macOS takes the pseudo terminal, Unix sockets, process groups, signals, `flock` and the paths from the Linux layers unchanged.
It differs where Linux reads `/proc`: process identity comes from `libproc` and `sysctl`, folder watching from FSEvents or kqueue, memory from `host_statistics64`, start at sign-in from a launchd agent, the credential store from the Keychain, and the window from WKWebView, which Wails already supports.
A release for it needs an Apple developer account for signing and notarization, and Mac runners.
It is filed as its own issue from this inventory once the Linux layers it reuses have landed.

## How it was measured

Every tracked Go file was classed by its file name and its `//go:build` line as Windows-only, built only off Windows, or built everywhere.
For each package, the names declared only in its Windows-only files were matched against the files built everywhere, in the same package and through its importers, and the untagged files were searched for Windows-only imports and `syscall` fields.
Names that collide with ordinary words can count a file twice or miss one, which is why the first Linux build in CI is the measure that counts.
