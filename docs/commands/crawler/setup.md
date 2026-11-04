# cfo crawler setup

`cfo crawler setup`

Set up the crawler connector every goblin carries, as each [cfo install](../install.md) does: write the build's `goblin-crawl` into `caches\crawler` in the home, which git ignores, install the packages its lockfile pins with `npm ci`, and only then write the `goblin-crawl` commands in `caches\crawler\bin`, which each goblin's terminal puts on PATH.
A connector already the build's is left as it is; one an earlier build installed is replaced.
It needs Node and npm on PATH, and the crawl itself needs Chrome; see [the crawler](../../crawler.md).
A failure exits 1 and says why, and goblins carry no crawler until a setup succeeds.
