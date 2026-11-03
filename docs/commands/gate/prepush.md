# cfo gate prepush

`cfo gate prepush [--plan] [--limit <duration>]`

The [delivery contract](../../contract/delivery.md#delivery) assigns this check to the selected validation owner.
It picks what the branch's change since it left the default branch can break, prints each check with why, and runs them one at a time from the repository's root until one fails, which ends the run with exit 1 and one last line naming the check and the tests that failed.
In order it checks with `go list -test` that every package the change reaches, as `cfo gate test` finds them, loads with its tests, which builds nothing and is where an import cycle shows.
The required guards follow [Verification levels](../../pipeline.md#verification-levels), including its platform and whole-package coverage rules.
The changed packages that are quick to test and the packages a contract names, each whole.
For a change under `frontend`, where `frontend/node_modules` is there, `npm run typecheck`, `npm run lint`, `npm test` and `npx playwright test` on the browser specs that changed, that open a fixture page that changed, that import a changed file beside them, or that share a word of their name with a changed source, as `run-terminal.spec.ts` does with `RunCard.tsx`.
Then the changed packages `config/verify.json` lists as slow, which come after the board's checks because a busy machine can spend the whole limit in them.
Then `go vet` of every package the change reaches, which is where a test of an importer that no longer builds shows, and the packages that import a changed one, nearest first.
A slow package runs without the tests this machine's own record has at 2 seconds or longer, except the tests in a test file the change touched, which run however long they take, and the tests under 15 seconds in the test file named for a source the change touched, as `spawn_test.go` is for `spawn.go`.
Such a package runs whole the first time it changes here, which is what times it, and is left to CI while it only imports the change and the record holds nothing of it.
Every run, and every `cfo gate test` run, adds how long each test it saw pass took to that record, which keeps a test's fastest pass so that a busy hour cannot turn a quick test into one every later run leaves out, under `cfo\verify\times` in the user's cache folder (`CFO_VERIFY_DIR` moves it).
The run takes the machine's turn in the line `cfo gate test` runs wait in, so the two never run together, tests one package at a time, waits within its time limit for free memory and commit to be over the 4 GB floor before it starts a check, saying once what it waits for, starts none after its time limit, 15 minutes of running unless `--limit` says otherwise, and ends a check still running at the limit, which is no failure, with everything the check started, a test's helper whose parent already exited included, naming each process it ended by name and pid.
A browser case that failed before the limit ended its check is a failure all the same, in a line naming the cases the check reached, because the browser check's own list reporter writes a row per case as the case ends, marked with its pass or fail, and the run reads those rows as it goes, while a case a retry then passed is no failure.
A test that fails runs once more by itself and fails the push only when it fails again, because a test that waits on a real terminal can miss its own deadline while another run holds this machine's processors, which CI would not see.
The run says which test that was.
That second run has five minutes of its own, past the limit when the limit ended the check, so whether a test failed never depends on where the limit fell, and a test whose second run does not end in that time fails the push in a line that says it failed and never passed.
Whatever it did not run it names under `left to CI`, each with why: the slow tests by number and time, the other browser specs, the board's build, and the checks the limit or the memory cut.
It exits 0 when no check failed, saying how many of the picked checks ran, or that none ran here when the pick holds nothing this machine can run, and 1 when one failed or the machine gave it no turn, when it says that nothing ran.
`--plan` prints the pick with each check's command and runs nothing.
The policy and its guards are read from the default branch as the branch last left it, so a branch cannot take a guard out of its own run.
It is this project's fast tier, so `cfo verify <task-id>` runs it; see [docs/pipeline.md](../../pipeline.md#before-a-push)
