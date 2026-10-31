# cfo defender

`cfo defender [--since <time\|day\|length>] [--json]`

List what Microsoft Defender recorded under the fleet's folders since a time, an RFC 3339 time, a day such as `2026-10-10`, or a length back from now such as `36h` or `7d` (a day by default): each detection with its threat's name and its files, each sample Defender sent to Microsoft with the first of its SHA-256, and for each file the test and the task that made it, read from the file's folders, or the gate run whose worktree it is in.
The fleet's folders are the home, the Dev Drive folder that holds its worktrees and scratch, `cfo` and `CodeGoblins` in the user's local application data, no-mistakes' own folder, and any Go test's temporary folder.
A detection that names no file, one of command lines, is listed with their count and never their text.
What Defender recorded elsewhere on the machine is counted and not named.
It only reads, with `Get-MpThreatDetection`, `Get-MpThreat` and the log `Microsoft-Windows-Windows Defender/Operational`: no scan, no restore, no setting, exclusion or allow list, which are the Supreme Overlord's alone.
A Defender that cannot be read fails the command with what it said, never a quiet report.
`--json` prints the same for a program.
The supervisor reads the same records every ten minutes and wakes you once for each new detection and each new upload (`defender_detection` and `defender_upload` in docs/native-board.md), so that one reaches you with the file and what made it, and not the Overlord alone as a Windows notice
