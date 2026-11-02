# cfo pr train

`cfo pr train <project>`

Land every green pull request goblins finished in the project with one CI run, as a [merge train](../../contract/delivery.md#merge-trains): merge them onto the default branch in queue order on a train branch, open a train pull request, and wait for its one CI run; on green merge the train pull request itself, so the branch takes the commit CI tested and GitHub marks each pull request that rode merged, and check the branch's tree equals the train's, on red run the failed checks again once, and on a second red halve the train until the pull request that breaks it is found, landing every half that passes, and send that pull request's goblin the failure.
A train that landed leaves nothing closed without merging on GitHub, and one that did not land its last run closes that run's pull request saying why.
Held (label `hold`), draft and conflicting pull requests never ride.
It joins the train already running in the project and exits when the train is over: 0 when it landed or stopped at the pull request that broke it, 1 when it failed
