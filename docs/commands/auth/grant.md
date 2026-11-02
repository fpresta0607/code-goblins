# cfo auth grant

`cfo auth grant <task-id> <service> [<service>...]`

Give a running task the credentials of more of its project's services, by their names in the manifest: the one way a task comes by a service after its spawn ([Credentials by need](../../contract/authentication.md#credentials-by-need)).
It names them in the task's record, so every later terminal of the task carries them, writes a `credentials:` record of the grant into the task's status log, regenerates the task's `auth.ps1` and sends its running terminal the one-line re-source notice.
Where that script cannot be regenerated, as while a refresh of the project holds the task, the record's change stands and the command says so and names the `cfo auth refresh <task-id>` that regenerates it.
It changes no other task.
A service the manifest does not declare is refused with the ones it does declare, and nothing is written.
It is the CFO's act: in a goblin's terminal or a gate agent's it is refused.
It prints service names, never a value

`cfo auth grant <task-id> --mcp <server> [<server>...]`

Give a running task MCP servers whose entry in the project's `.mcp.json` holds a value, by name: the one way a task comes by such a server after its spawn ([Credentials by need](../../contract/authentication.md#credentials-by-need)).
It names them in the task's record and writes a `credentials:` record of the grant into the task's status log.
A harness reads its MCP servers when it starts, so the task is given them at its next terminal, and the command prints the `cfo switch <task-id> --restart` that starts one.
It changes no other task.
A server the project's `.mcp.json` does not define is refused with the ones it does define, and nothing is written.
It is the CFO's act, refused in a goblin's terminal and a gate agent's.
It prints server names, never a value

`cfo auth grant <task-id> --codex-credits`

Authorize a running task to spend paid Codex credits, on the Overlord's word: while Codex's included allowance is used up, its [codex guard](../../contract/dispatch.md#pausing-and-automatic-resume) stays disarmed, so a Codex session its terminal starts runs and bills them.
It records that on the task's record and writes a `credentials:` record into its status log, and the supervisor follows the record at its next fleet reading, within a minute, for every terminal of the task, the running one included.
It changes no other task, a helper the task started included.
It is the CFO's act: refused in a goblin's terminal, a gate agent's and another agent's session
