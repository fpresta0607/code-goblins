# cfo ci-runner

`cfo ci-runner job \| status \| parity --repo <owner/name>`

Run one CI job in this machine's desktop CI runner, the `cg-ci-runner` service, which runs one GitHub Actions job at a time in a Hyper-V VM put back to clean before every job.
The watcher starts `job` itself, in a turn at the lowest priority of the machine's line expected to use 7 GiB, for each job the runner says waits, one at a time, so a goblin never runs it.
It says the job's progress on standard error and its outcome on standard output, exits 0 when the job ran, 3 when no job came for the runner and 1 otherwise, and ending it ends the job and puts the VM back.
`status` says whether a job runs and how the last one ended.
`parity` adds to the repository's parity record each job its desktop mirror ran beside the hosted job it copies, on the same merge, and says for each job how many runs in a row matched, and when 20 have, that the hosted job may retire on his word.
See [docs/pipeline.md](../pipeline.md#a-desktop-ci-job-in-the-line)
