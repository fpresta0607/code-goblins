## Project local services

A goblin whose full-stack check needs a project's local services, such as a backend with its Redis and Qdrant, asks cfo for them rather than running Docker itself.
You reach them the same way, so no separate Docker MCP server is needed.
A project declares them in `data/projects/<name>/services.json`, beside its auth manifest:

```json
{
  "project": "northwind",
  "compose": "docker-compose.dev.yml",
  "env_file": ".env.docker.local",
  "services": ["backend"],
  "check": ["pwsh", "-NoProfile", "-File", "scripts/check-dev-env.ps1"],
  "memory_estimate_gb": 3
}
```

- `compose` and `env_file` are paths inside the project's main checkout, where the stack runs: one stack per project, shared by every task that holds it, built from the checkout and never from a goblin's worktree.
- `services` are the compose services a full-stack check needs, and compose starts what they depend on with them.
- `check`, when set, runs in the checkout before anything starts, and a check that exits non-zero refuses the start.
  It is the place for the project's guard that its env file names no production service.
- `memory_estimate_gb` is what the stack is expected to cost before cfo has measured it.

`cfo services up <project> --task <id>` starts the stack for a live task, or adds the task to the tasks holding it when it is up.
A start first takes the machine's one turn for heavy runs ([Heavy local runs](dispatch.md#heavy-local-runs)), so no test suite or other build starts beside its build, and holds it until the stack is up or the start has failed.
Joining a stack that already runs takes no turn, since it starts nothing.
It waits for the turn with no limit of its own and says why as it waits, and a start run inside `cfo gate turn` runs inside that wrapper's turn.
It starts Docker Desktop's engine first when the engine is not running, and starts anything only while free memory and free commit stay at or above the 4 GB floor with the stack's cost added: its last measured cost, or the estimate before the first.
Until they would, it says why once a minute and waits, for up to `--wait` (30 minutes by default, and `0` refuses at once).

Docker Desktop 4.86 opens its dashboard window as it starts while its setting "Open the Docker Dashboard when Docker Desktop starts" is on, and the Overlord's settings leave it at Docker Desktop's default, which opened the dashboard on every start on record.
`docker desktop start` takes no other word for it, and neither does Docker Desktop's launcher or the window process its backend starts with `--name=dashboard`.
That setting is the Overlord's, and cfo changes no Docker Desktop setting.
So an engine start looks every 20 ms, from before the start, for the window process that start started: `Docker Desktop.exe` started after the start began, with a `--reason=` and no `--type=`, which tells it from the launcher and from Electron's helpers.
Its dashboard is the top-level window of that process made with Electron's window class and titled `Docker Desktop`, and cfo asks that window to close as its close button would, which leaves Docker Desktop in the tray and its engine running.
Docker Desktop makes that window hidden and shows it once it has drawn it, 200 ms later on 2026-10-11, so cfo closes it before it ever shows, or as soon as it finds it if it has shown, and says which, with the pid.
The process's other windows, which keep it in the tray and carry no title, are never asked, no other process's window is, and no process is ended.
It looks for at most 30 seconds after the engine answers.
Docker Desktop then writes `Failed to open UI ...
Object has been destroyed` to its Electron log and sends that error to its own error reporting, which its analytics setting allows.
Its code registers the window and its cleanup before a close can reach it, so its tray still opens a new dashboard when asked.
With the setting turned off the window process starts with no `--name=dashboard`, and cfo closes nothing.

While compose up builds and starts the stack, the start holds the floor itself, as cg-services-pd-build's watch beside it did on 2026-10-11.
It reads free memory and free commit every 2 seconds, and the first reading under the 4 GB floor ends compose up with everything it started, so its build and nothing else, and the start then stops what it started as any failed start does.
Every 10 seconds, one relief at a time, it hands the file cache of Docker's virtual machine back with `wsl.exe -d docker-desktop -u root -- sh -c "sync && echo 3 > /proc/sys/vm/drop_caches"`.
A build fills that cache with every file it writes, and Windows counts all of it as used until the virtual machine hands it back, slowly: PrecisionDocs' second build reached the floor on 2026-10-10 in a burst of file copies alone.
The build the watch guarded never fell under 9.21 GB free, where the two before it reached the floor, though how much of that was the relief is unmeasured.
A relief that fails is said once, and the floor watch goes on without it.

A start whose compose up fails, rather than one cfo ended at the floor, first saves what each service that did not come up says of itself, since the stop that follows takes its container and its log with it.
For each container that ran and stopped, keeps restarting or runs without passing its health check, it saves the state `docker inspect` reads, with the health check's last output, and the last 500 lines of `docker logs`, both streams in the order written.
A read docker cannot give, as of a log its logging driver does not keep, is said in its place, and the other containers are saved all the same.
They go to `services-<project>-<time>.log` in the task's folder under `state\tasktmp\`, and the start's failure names that file, or says why nothing could be saved.
On 2026-10-20 a backend came up unhealthy twice, and each stop left nothing to read why.

A start measures the drop in free memory while the stack, and the engine when it started that too, came up, a stop measures the rise, and the record keeps the larger, so the next start is admitted on what the stack really costs.
A start cut short, as the floor ends one part way through a build or as its process is ended from outside, never measures itself, and Docker's virtual machine gives its memory back slowly, so the stop that follows also measures the drop from the memory read before that start began.
A first start builds the images the stack lacks inside that start, which can take many minutes and more memory than the stack runs on: PrecisionDocs' backend build took 6.6 GB with the engine on 2026-10-08, where redis and qdrant with the engine took 3.4 GB.
The cost it measures guards every later start, and a later start that finds the images measures what the running stack takes instead.
A stack that was already running when cfo came is shared and never stopped by cfo, and beside one that runs part of the stack cfo stops only the services it started.
`cfo services down <project> --task <id>` releases the hold, and the last release stops what cfo started, keeping named volumes, and the engine when cfo started it and nothing it did not start runs on it.

`cfo cleanup`, which `cfo kill` runs too, releases every stack the task held.
A hold ends with the task's record, so a paused goblin keeps its stack for when it resumes, and the janitor stops a stack no live task holds.
The record of every stack, its holders and its measured cost is `state\services.json`.
`cfo runtime` lists each stack under Local services, with who holds it, what cfo started of it and the memory it was measured to take, and the board's Workspace panel says the same: a goblin's names the stack it holds, and the CFO's every stack held.

A brief whose task needs its project's local services says so with the line `services: needed` in its `## Delivery` section.
`cfo spawn` refuses such a brief for a project that declares none, before anything is built, and refuses a `services:` line with any other value.
Otherwise it tells the goblin the one command that holds the stack and the one that releases it, and its `spawned` output says so on a `services:` line.
