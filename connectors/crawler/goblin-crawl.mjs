#!/usr/bin/env node
import path from "node:path";
import { parseArgs } from "node:util";

import { crawlSite, DEFAULT_MAX_PAGES, MAX_IN_FLIGHT } from "./crawl-site.mjs";

const USAGE = `usage: goblin-crawl <url> [--out <folder>] [--max-pages <n>]

Finds every route of the site at <url>, from its sitemaps and from the links
of every page as headless Chrome draws it, reads each page, and downloads
every file the pages link to. It writes report.md and report.json, each page's
facts and HTML under pages/, and the files under files/, in --out, which is
crawl-<host> in this folder unless named. It obeys robots.txt, fetches at most
${MAX_IN_FLIGHT} pages or files at a time, and names itself in its user agent.

--max-pages  the most pages it reads, ${DEFAULT_MAX_PAGES} unless named; the report names each route past it`;

async function main(argv) {
  let parsed;
  try {
    parsed = parseArgs({ args: argv, allowPositionals: true, options: { out: { type: "string" }, "max-pages": { type: "string" }, help: { type: "boolean", short: "h" } } });
  } catch (error) {
    process.stderr.write(`goblin-crawl: ${error.message}\n${USAGE}\n`);
    return 2;
  }
  const { values, positionals } = parsed;
  if (values.help) {
    process.stdout.write(USAGE + "\n");
    return 0;
  }
  if (positionals.length !== 1) {
    process.stderr.write(USAGE + "\n");
    return 2;
  }
  const maxPages = values["max-pages"] === undefined ? DEFAULT_MAX_PAGES : Number(values["max-pages"]);
  if (!Number.isInteger(maxPages) || maxPages < 1) {
    process.stderr.write(`goblin-crawl: --max-pages takes a whole number of at least 1, not ${values["max-pages"]}\n`);
    return 2;
  }
  let host;
  try {
    host = new URL(positionals[0]).host;
  } catch {
    process.stderr.write(`goblin-crawl: ${positionals[0]} is not an address\n`);
    return 2;
  }
  const outDir = path.resolve(values.out ?? `crawl-${host.replace(/[^A-Za-z0-9.-]+/g, "-")}`);
  try {
    const report = await crawlSite({ startUrl: positionals[0], outDir, maxPages });
    const { pages_read: pages, files_downloaded: files, unread } = report.counts;
    process.stdout.write(`goblin-crawl: pages read: ${pages}, files downloaded: ${files}, routes unread: ${unread}; report:${path.join(outDir, "report.md")}\n`);
    return 0;
  } catch (error) {
    process.stderr.write(`goblin-crawl: ${error.message}\n`);
    return 1;
  }
}

process.exitCode = await main(process.argv.slice(2));
