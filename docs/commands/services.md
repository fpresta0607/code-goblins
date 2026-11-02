# cfo services

`cfo services up <project> --task <id> [--wait <duration>]` \| `down <project> --task <id>`

Start the local services a project declares for a task's full-stack check, or share them with the tasks that already hold them.
A start takes the machine's one turn for heavy runs, then starts only while free memory and commit stay at or above the 4 GB floor with the stack's measured cost added, waiting up to `--wait` and saying why.
While compose up builds it holds the floor itself: it hands the engine's file cache back every 10 seconds and ends its own build, and nothing else, at the first reading under the floor.
A start whose compose up fails saves each failed service's state, health check and log tail to the task's folder before it stops, and names the file.
An engine start closes the dashboard window Docker Desktop opens as it starts.
`down` releases the hold, and the last release stops what cfo started, and the Docker engine when cfo started it ([Project local services](../contract/services.md#project-local-services))
