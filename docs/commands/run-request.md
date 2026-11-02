# cfo run-request

`cfo run-request --id <stable-id> --title "<why>" --shell powershell|pwsh|bash [--admin] [--cwd <dir>] --command-file <path>`

registered CFO asks the Overlord to run a command with one click in the Command Center, where it runs in a terminal on its card that he types into; the file is read once and runs as a script file, and the exit code and the end of what its terminal showed come back as his answer; --admin runs it elevated after Windows asks him to confirm

`cfo run-request --withdraw <id> --reason "<why>"`

registered CFO takes a run item nobody ran off the Command Center, audited in state/runs.audit; Run on it is refused from then on, and a replacement is a new item under a new ID

Read the [applicable decisions procedure](../contract/decisions.md) before acting.
