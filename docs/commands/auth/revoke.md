# cfo auth revoke

`cfo auth revoke <task-id> --codex-credits`

Withdraw a task's authorization to spend paid Codex credits, so its codex guard refuses Codex again while Codex's included allowance is used up, from the supervisor's next fleet reading.
It writes a `credentials:` record into the task's status log, and is refused in a goblin's terminal and a gate agent's

`cfo auth revoke <task-id> <service> [<service>...]`

Take services back from a task, as when a grant was for one step of it ([Credentials by need](../../contract/authentication.md#credentials-by-need)).
It takes them out of the task's record, so no later terminal of the task carries them, writes a `credentials:` record of the revoke into the task's status log and regenerates the task's `auth.ps1` without them.
A terminal running then keeps what it already holds of them, the values it started with and any it loaded from that script, until it ends, since nothing takes a variable back out of a running process.
The command says so and prints the `cfo switch <task-id> --restart` that starts a terminal without them, and it sends the running terminal no notice, since loading that script takes no service's variable away.
Where that script cannot be regenerated, the record's change stands, so the command says so and names the `cfo auth refresh <task-id>` that regenerates it, since the same revoke run again finds nothing to take.
It changes no other task, a helper the task started included.
A service the task does not carry, whether or not the manifest declares it, is refused with the ones it does carry, and nothing is written.
It is the CFO's act, refused in a goblin's terminal and a gate agent's.
It prints service names, never a value

`cfo auth revoke <task-id> --mcp <server> [<server>...]`

Take MCP servers back from a task, by their names in its record.
It takes them out of the task's record, so its next terminal is given none of them whose entry holds a value, and writes a `credentials:` record of the revoke into the task's status log.
A harness reads its MCP servers when it starts, so the one running then keeps them until it ends, and the command prints the `cfo switch <task-id> --restart` that starts one without them.
It changes no other task.
A server the record does not name is refused with the ones it does name, and nothing is written.
It is the CFO's act, refused in a goblin's terminal and a gate agent's.
It prints server names, never a value
