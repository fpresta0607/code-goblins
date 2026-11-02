# cfo pr train

`cfo pr train <project>`

Start a merge train for the green pull requests goblins finished in the project.
See the [merge-train contract](../../contract/delivery.md#merge-trains) for rider eligibility, green-run holds, retries, landing and recovery.
It joins the train already running in the project and exits when the train is over: 0 when it landed or stopped at the pull request that broke it, 1 when it failed
