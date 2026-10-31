# cfo update

`<candidate> update [--recover]`

Install a verified candidate build by running the candidate itself: it backs up the build installed, swaps `cfo.exe` and `goblins.exe` where the home keeps them, in `bin`, or at the root of a home a build before `bin` set up, such as a checkout an older build made the home, for the candidate as one journaled step, restarts only the supervisor and proves the new one serves, and otherwise puts the previous build back and proves that one serves; terminal hosts, goblins and the CFO keep running.
See the README's build update guidance for build retention.
Exit 0 installed, 3 rolled back, 4 board down, 5 serving from a verified copy with the aliases still to repair, and 1 when it refuses with nothing changed: another update of the home runs, the last journal cannot be trusted or is an unfinished update of another home (a finished one, whatever home it names, is history), it ran past a minute before it changed anything, or the watcher lock is held by a supervisor it cannot prove is this home's.
An earlier update of this home that stopped part way is put back first, by the update itself.
Before it stops the supervisor it waits up to 15 seconds for lifecycle work in flight (a start, a pause, a resume, a clean-up), and while it installs, `cfo spawn`, `switch`, `pause`, `resume`, `kill`, `cleanup` and `helper` refuse with one sentence and change nothing: run the command again a minute later.
It holds the watcher lock from the supervisor it stopped to the one it starts, so no other supervisor starts in between, and it is bounded: past a minute with the board away it is stopped and the previous build put back.
It prints nothing to paste, and `--recover` stays for recovery by hand ([docs/native-board.md](../native-board.md#update-code-goblins))

`goblins update [--check] [--to <tag> [--run <item>]]`

The Supreme Overlord's alone, never yours, in AFK mode or out of it: run as the home's own `goblins` or `cfo`, it updates the home from the newest published release of Code Goblins.
It downloads the release's `cfo.exe` and desktop window into `state\update`, keeps each only when it matches the release's `SHA256SUMS` and, for a release whose `install.ps1` names a publisher, carries that publisher's valid signature, runs the downloaded build's own `update` above, then that build's `install` to bring the home's contract, skills and hooks up to date where the machine's install names this home, and ends on `Updated:`, `Rolled back:` or `Failed:` with why.
Exit 6 means the new build serves but the home refresh failed, or the machine's installed home could not be read: it keeps that build, ends on `Updated:` with what remains to do, and says to run `goblins install` to finish; deliberately skipping another home's refresh exits 0.
It refuses under the registered CFO, in a goblin's or a gate agent's terminal, under an agent harness and where its parents do not reach the desktop.
`--check` only says how this build stands against the newest release and what is new, and is yours to run; `--to <tag>` refuses when a newer release was published since.
A build made from a clone is told to pull and run `.\install.cmd -Dev` instead.
For each newer release the board makes the Update item in the Command Center ([docs/native-board.md](../native-board.md#update-code-goblins)); only he presses it, from a board of his own, which runs this command with `--run <item>` on a one-time grant in place of his terminal.
You never press it, withdraw it (`cfo run-request --withdraw` refuses it) or ask him to; in AFK mode it waits for him in the Command Center
