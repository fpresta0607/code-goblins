# The crawler connector

Every goblin carries a crawler: `goblin-crawl`, a command on its terminal's PATH that finds every route and file of a site and reads each page as a browser draws it.
It is the crawl step of the `reverse-engineer-site` skill (`~/.agents/skills/reverse-engineer-site/`), which turns a site a brief names into an inventory a rebuild can start from.
It needs no paid service, account or key, runs no process while it is idle, and starts headless Chrome only while it crawls.

If you run a site and saw `CodeGoblinsCrawler` in your logs: it is this command, run by a person's agent on their own machine.
It obeys your robots.txt, sends at most three requests at a time, and says who it is on every request.
A `Disallow` line for `CodeGoblinsCrawler`, or for every agent, keeps it out.

## What it does

```
goblin-crawl <url> [--out <folder>] [--max-pages <n>]
```

- It reads the site's `robots.txt` first and never requests a path it disallows.
  The browser's own requests are held to it too: the crawler sends each request a page's script, style or link makes itself, from the page, a frame in it or a window it opens, and never one to a disallowed path, one that reaches the site through a redirect from another site included.
  It follows every redirect one step at a time, the browser's included, and never into a disallowed path, and it turns a request into a GET without its body where a browser would.
  It lets no page start a service worker, whose requests the browser would send unseen, or open a WebSocket or an event stream to the site, which would hold one of its requests for as long as the page is open.
  A page that redirects within the site is queued as the page it redirects to, and one that redirects off the site or into a disallowed path is reported unread.
  A page that redirects to itself, as one that sets a cookie first does, is followed until it answers, and routes that redirect to each other are reported unread.
  A site that answers robots.txt with a server error, or not at all, is not crawled, and one that has none, or answers 4xx, allows everything, as the robots.txt standard says.
- It reads every sitemap robots.txt names and `/sitemap.xml`, sitemap indexes included, on the site only and at most 50 of them.
  The report lists each sitemap it did not read, with why: past the sitemap limit of 50, it is on another site, robots.txt disallows it, or the site did not serve it.
  A route only an unread sitemap names is not found, the same trade as the page limit.
- It opens each page in headless Chrome, waits for the page to settle, and reads it as drawn, so a route only a script links to is found and a page a script fills is read with its content.
  It loads no image, font or video while it does: it records each image's address and alt text instead, and asks the site for less.
- It follows every link on the site and lists every link off it without following it.
- It downloads every file the pages link to, such as PDFs, documents, archives and images.
- It starts no process but its browser: it keeps its requests in flight whatever the machine's load, rather than measuring the load by listing the machine's processes, as Crawlee would.
- It sends at most 3 requests to the site at a time, pages and downloads alike, as `CodeGoblinsCrawler/<version> (+https://github.com/fpresta0607/code-goblins/blob/main/docs/crawler.md)`, the browser's own requests included.
  Each request the browser makes, from whichever page it has open, is sent by the crawler one redirect step at a time, and each step to the site waits its turn, one a redirect from another site sends there included.
- It reads at most 500 pages unless `--max-pages` says otherwise, counting the pages it reads, never a redirect or a file.
- It reports every route it could not read, with why: robots.txt disallows it, it redirects into a path robots.txt disallows, the site answered an HTTP error, the connection to the site failed, with the error it failed with, it redirects off the site, it redirects in a loop or more than 20 times, it is past the page limit, or it did not load in time.

It writes, in `--out`, which is `crawl-<host>` in the current folder unless named:

- `report.md` and `report.json`: every route with its kind, its status, how it was found and the file it was saved as, then the unread routes, the sitemaps and the links off the site, with counts.
  A route that redirects within the site is a `redirect` to the page it names, which is read, and counted, once, by its own address.
- `pages/`: each page's rendered HTML and a JSON file of its facts: title, description, language, headings, text, tables, images, links and layout landmarks.
- `files/`: every file it downloaded, each under a name of its own, its number in the crawl before its address.

It exits 0 when it crawled, 1 with one line saying why when nothing could be crawled, and 2 for a usage mistake.

## How a goblin gets it

- The build carries the connector's code and the lockfile that pins every package it installs, under `connectors\crawler` in this repository.
- [`cfo install`](commands/install.md), which `install.ps1` and the board's **Update** both run, sets the connector up as [`cfo crawler setup`](commands/crawler/setup.md) does: it writes them into the home's `caches\crawler`, installs the pinned packages with `npm ci`, and only then writes `goblin-crawl.cmd`, and a `goblin-crawl` script for Git Bash, in `caches\crawler\bin`.
  Git ignores the home's caches, so a home that is a checkout of this repository keeps its `connectors\crawler` as git has it.
  An install of a newer build replaces a connector an earlier build installed, and leaves one already the build's alone.
  It needs Node.js, which the fleet's other tools need too, and the crawl needs Chrome.
  A setup that runs past two minutes, as an `npm ci` waiting on a registry it cannot reach does, is stopped with every process it started, so the install ends inside the board's **Update** bound of five minutes.
  A connector it cannot set up never fails the install: a `Note:` line says goblins go without `goblin-crawl` until `cfo crawler setup` succeeds, and on a machine without Node.js the install script's closing note names `crawler`.
- `cfo spawn` puts `caches\crawler\bin` on every goblin's PATH, after its codex guard folder and before the user's PATH, whichever harness it runs, with no word of it in the brief.
  It is a command rather than an MCP server because Claude Code goblins are the only goblins given MCP servers: a command reaches Codex and pi goblins too.
- Each goblin's **Connections** on the board lists `crawler` under **Default connectors**: provided when its terminal finds `goblin-crawl`, disabled when its brief left it out, and missing when the home never set it up, or set it up after the goblin's terminal started, which a resume, switch or respawn carries.
- `cfo doctor` says whether the home's connector is ready, older than the build, or not set up, with the fix.

## Leaving it out

A brief leaves the crawler out with one line in its Authentication section, beside its `credentials:` and `mcp:` lines:

```
## Authentication

credentials: none
mcp: none
connectors: none
```

- `connectors: none` gives the goblin a terminal whose PATH has no `goblin-crawl`, and its task record keeps the choice, so a resume or a switch leaves it out too.
- No `connectors:` line carries it.
- Any other value, `connectors: crawler` included, or a second `connectors:` line, is refused by `cfo brief ready` and by the spawn.
- A helper never carries more than its parent: the helper of a goblin without the crawler goes without it, whatever its own brief says.

## Why Crawlee

The connector is a thin command over [Crawlee](https://github.com/apify/crawlee), Apify's crawling library for Node.js, driving the machine's own Chrome through [Playwright](https://github.com/microsoft/playwright).
Surveyed on 2026-11-03, with stars, licence and latest release read from GitHub:

| Tool | Stars | Licence | Latest release | Windows | Draws JavaScript | Sitemap and link discovery | File downloads | Agent-readable output | Paid service, account or key |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| [Crawlee](https://github.com/apify/crawlee) | 26.3k | Apache-2.0 | v3.18.2, 2026-09-29 | Yes, on Node.js | Yes, through Playwright or Puppeteer | Yes: sitemap and robots.txt helpers, link queueing | Yes | JSON datasets, and the rendered page to read | No |
| [Crawlee for Python](https://github.com/apify/crawlee-python) | 9.6k | Apache-2.0 | v1.11.0, 2026-10-15 | Yes, on Python | Yes, through Playwright | Yes | Yes | JSON datasets | No |
| [Crawl4AI](https://github.com/unclecode/crawl4ai) | 86.9k | Apache-2.0 with an added attribution requirement | v0.9.5, 2026-10-14 | Yes, on Python with Playwright's own browsers | Yes | Yes: deep crawls and sitemap seeding | Yes | Markdown and JSON made for language models | No; only its optional model-based extraction takes a model key |
| [Firecrawl](https://github.com/firecrawl/firecrawl), self-hosted | 198.3k | AGPL-3.0 | v2.11.0, 2026-06-19 | Only in Docker, with Redis and a browser service | Yes | Yes: its map endpoint | PDFs and documents, turned into Markdown | Markdown and JSON | No when self-hosted; its cloud takes an account and a key |
| [Scrapy](https://github.com/scrapy/scrapy) | 65.2k | BSD-3-Clause | 2.19.0, 2026-09-10 | Yes, on Python | No, without the scrapy-playwright plugin | Yes: sitemap and link-following spiders | Yes: its files pipeline | The items you define, as JSON or CSV | No |
| [Katana](https://github.com/projectdiscovery/katana) | 17.8k | MIT | v1.8.0, 2026-10-05 | Yes, one prebuilt program | Yes, in its headless Chrome mode | Yes, robots.txt and sitemaps read as sources of routes, which it does not obey | No: it lists file addresses | JSON lines of endpoints, not page content | No |
| [spider](https://github.com/spider-rs/spider) | 2.8k | MIT | v2.53.9, 2026-10-30 | Yes, built from source with Rust | Yes, when built with its Chrome feature | Yes, with its sitemap feature | Yes, from its command line | HTML, and Markdown through its transformations crate | No |
| [Playwright MCP](https://github.com/microsoft/playwright-mcp) | 39.1k | Apache-2.0 | v0.0.84, 2026-10-31 | Yes, on Node.js | Yes: it is a browser | No: the agent drives one page at a time | Through the browser | Accessibility snapshots | No |
| [Chrome DevTools MCP](https://github.com/ChromeDevTools/chrome-devtools-mcp) | 54.7k | Apache-2.0 | v1.12.0, 2026-10-22 | Yes, on Node.js | Yes: it is a browser | No: the agent drives one page at a time | Through the browser | Snapshots and page text | No |
| [Firecrawl MCP server](https://github.com/firecrawl/firecrawl-mcp-server) | 7.8k | MIT | v3.36.1, 2026-11-03 | Yes, on Node.js | Through Firecrawl | Through Firecrawl | Through Firecrawl | Markdown and JSON | Yes: a Firecrawl key, or a self-hosted Firecrawl |
| [Fetch MCP server](https://github.com/modelcontextprotocol/servers/tree/main/src/fetch) | 91.8k, for the whole repository | MIT, moving to Apache-2.0 | v2.2.0 of the repository, 2026-10-28 | Yes, on Python | No | No: one address at a time | No | Markdown | No |

Crawlee is the pick because it is the one that does everything the fleet needs where the fleet already runs:

- Its licence lets Code Goblins install it for anyone, with no term beyond Apache-2.0's, and every package its lockfile pins is under MIT, Apache-2.0, ISC, BSD or a licence as permissive.
  Firecrawl's AGPL and Crawl4AI's added attribution requirement put terms on every user that the fleet would pass on.
- It runs on Node.js, which every fleet machine has for its other tools, and drives the Chrome the machine already has, so the install downloads no browser and no other runtime.
  The Python tools would add Python packages and Playwright's own browsers to every machine, and Firecrawl would add Docker.
- It draws pages a script fills, which Scrapy cannot without a plugin, and its sitemap and robots.txt helpers do the discovery, so the command stays small.
- It is a library, so the command decides how it behaves: it obeys robots.txt, keeps three requests in flight and names itself, all by default, and starts Chrome only while it crawls.
  Katana is a strong route finder, but it is built for security reconnaissance and reads robots.txt as a list of places to look.
- The MCP servers drive a browser one page at a time, or hand the crawl to a hosted service, and an MCP server reaches only Claude Code goblins, where a command on PATH reaches every harness.

The connector pins `@crawlee/playwright`, `@crawlee/utils` and `playwright-core` at exact versions in `connectors\crawler\package.json`, and its lockfile pins everything they bring, as `install.ps1` pins the fleet's other tools.
To move to newer versions, change the pins there, run `npm install` in that folder so the lockfile follows, and run its tests with `npm test`; the next install brings the new connector to every home.

## Its tests

`npm test` in `connectors\crawler` crawls a made-up site, `fixture-site.mjs`, served on loopback: a robots.txt that names a sitemap on another hostname, a sitemap index that names 56 sitemaps on the site and one on a second site, a page only the sitemap names, two pages only an inline SVG links to, by `href` and by `xlink:href`, a page whose only link to a product page is drawn by its script, which also asks for data in a folder robots.txt disallows, directly and through a redirect, tries to start a service worker that would ask for more, posts a search a redirect answers, and opens an event stream, a WebSocket and a window into the disallowed folder, the second site, on another port, from which that page loads a script that redirects into the disallowed folder, a page that redirects to one the sitemap names and one that redirects into the disallowed folder, a page that redirects to itself until it is asked with the cookie it sets, two routes that redirect to each other, a page whose script never stops asking for data, a page whose connection the site drops, a table, an image, linked PDFs, two of them at addresses that differ only in a slash and one that redirects into the disallowed folder, pages that each load a stylesheet and three scripts, and a link off the site.
The tests check that the crawl finds and reads every route, an inline SVG's links included, asks for no more than 50 sitemaps and lists each other one, and the ones robots.txt and the index name on other sites, unread with why, reads the page a redirect names once, by its own address, starts no process but its browser, downloads each PDF byte for byte under a name of its own, never requests what robots.txt disallows, the script's requests, the service worker, the window, the second site's redirect and every redirect included, reports the routes that redirect into the disallowed folder unread, reads the page that redirects to itself, reports the redirect loop unread, reports why the page whose connection the site drops went unread, follows the search's redirect as a GET, opens no event stream or WebSocket, names itself on every request, the browser's included, keeps at most three requests in flight, the browser's own for each page's scripts included, reads every page after the one that never settles at its first navigation, counts only pages read against the page limit, a page read again after its reading failed once, and lists every unread route in its report.
CI runs them in the frontend job, and the Go tests prove each harness's goblin finds `goblin-crawl` on its PATH and that `connectors: none` leaves it out.
