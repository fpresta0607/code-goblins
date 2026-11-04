import assert from "node:assert/strict";
import childProcess from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { after, before, describe, test } from "node:test";

import { RobotsTxtFile } from "@crawlee/utils";

import { crawlSite, MAX_IN_FLIGHT, USER_AGENT } from "./crawl-site.mjs";
import { PDF, startFixtureSite } from "./fixture-site.mjs";

// Every way Node starts a process, watched while the fixture site is crawled.
const PROCESS_STARTS = ["spawn", "spawnSync", "exec", "execSync", "execFile", "execFileSync", "fork"];

// Each crawl starts a headless Chrome, so the fixture site is crawled once
// and each test reads what that crawl did.
describe("a crawl of the fixture site", () => {
  let site;
  let outDir;
  let report;
  const started = [];
  const route = (pathname) => report.routes.find((entry) => new URL(entry.url).pathname === pathname);

  before(async () => {
    // Arrange
    site = await startFixtureSite();
    outDir = await mkdtemp(path.join(os.tmpdir(), "goblin-crawl-"));
    const originals = Object.fromEntries(PROCESS_STARTS.map((name) => [name, childProcess[name]]));
    for (const name of PROCESS_STARTS) {
      childProcess[name] = (command, ...rest) => {
        const result = originals[name](command, ...rest);
        started.push({ command: String(command), pid: result?.pid });
        return result;
      };
    }

    // Act
    try {
      report = await crawlSite({ startUrl: site.url, outDir });
    } finally {
      Object.assign(childProcess, originals);
    }
  });

  after(async () => {
    await site?.close();
    if (outDir) await rm(outDir, { recursive: true, force: true });
  });

  test("reads every route, those only the sitemap or a script names included", () => {
    const read = report.routes.filter((entry) => entry.kind === "page" && entry.status === "read").map((entry) => new URL(entry.url).pathname).sort();
    assert.deepEqual(read, ["/", "/about", "/catalog", "/catalog/box-kite", "/workshops"]);
    assert.deepEqual(route("/workshops").found_by, ["sitemap"]);
    assert.deepEqual(route("/catalog/box-kite").found_by, [new URL("/catalog", site.url).href]);
  });

  test("reads the page a script draws as the browser shows it", async () => {
    const facts = JSON.parse(await readFile(path.join(outDir, route("/catalog").page), "utf8"));
    assert.match(facts.text, /Box kite/);
    assert.ok(!facts.text.includes("Loading..."), "the catalog was read before its script drew it");
  });

  test("reads each page's tables, images and layout", async () => {
    const facts = JSON.parse(await readFile(path.join(outDir, route("/about").page), "utf8"));
    assert.deepEqual(facts.tables, [[["Day", "Hours"], ["Saturday", "9 to 17"]]]);
    assert.deepEqual(facts.images, [{ src: new URL("/images/workshop.jpg", site.url).href, alt: "The workshop" }]);
    assert.deepEqual(facts.layout.map((element) => element.tag), ["header", "nav", "main"]);
  });

  test("downloads every file a page links to", async () => {
    const pdf = route("/docs/price-list.pdf");
    assert.equal(pdf.kind, "file");
    assert.equal(pdf.status, "downloaded");
    assert.deepEqual(await readFile(path.join(outDir, pdf.file)), PDF);
  });

  test("obeys robots.txt: a disallowed route is never asked for and is reported unread", () => {
    assert.ok(site.requests.some((request) => request.path === "/robots.txt"), "the crawl never read robots.txt");
    assert.ok(!site.requests.some((request) => request.path.startsWith("/private/")), "the crawl asked for a route robots.txt disallows");
    assert.equal(route("/private/notes").status, "unread");
    assert.equal(route("/private/notes").reason, "robots.txt disallows it");
  });

  test("names itself honestly on every request, the browser's own included", () => {
    assert.ok(site.requests.some((request) => request.path === "/catalog.js"), "the browser asked for no script, so this test read only the crawler's own requests");
    for (const request of site.requests) {
      assert.equal(request.userAgent, USER_AGENT, `${request.path} was asked for as ${request.userAgent}`);
    }
    assert.match(USER_AGENT, /^CodeGoblinsCrawler\/\d+\.\d+\.\d+ \(\+https:\/\/github\.com\/fpresta0607\/code-goblins\/blob\/main\/docs\/crawler\.md\)$/);
  });

  test("starts no process but its browser, and ends that by its pid", () => {
    const browsers = started.filter((start) => /^chrome(\.exe)?$/i.test(path.basename(start.command)));
    assert.ok(browsers.length > 0, `the crawl started no Chrome this test saw, so it watched nothing (${started.map((start) => start.command).join(", ")})`);
    // Playwright ends a browser that outlives its close, on Windows, with
    // taskkill and the browser's pid.
    const browserPids = new Set(browsers.map((browser) => String(browser.pid)));
    const endsABrowser = (command) => browserPids.has(/^taskkill \/pid (\d+) \/T \/F$/.exec(command)?.[1]);
    const others = started.filter((start) => !browsers.includes(start) && !endsABrowser(start.command));
    assert.deepEqual(others.map((start) => start.command), [], "the crawl started a process that is neither its browser nor the end of one");
  });

  test("records links off the site without following them", () => {
    assert.deepEqual(report.external_links, ["https://example.org/partner"]);
  });

  test("writes a report that names every route and why one went unread", async () => {
    const markdown = await readFile(path.join(outDir, "report.md"), "utf8");
    for (const entry of report.routes) assert.ok(markdown.includes(entry.url), `report.md does not name ${entry.url}`);
    assert.match(markdown, /- http:\/\/127\.0\.0\.1:\d+\/private\/notes: robots\.txt disallows it/);
  });
});

test("a crawl keeps at most a few requests in flight at a time", async () => {
  // Arrange
  const site = await startFixtureSite({ delayMs: 200, extraPages: 12 });
  const outDir = await mkdtemp(path.join(os.tmpdir(), "goblin-crawl-"));

  try {
    // Act
    const report = await crawlSite({ startUrl: site.url, outDir });

    // Assert
    assert.equal(report.counts.pages_read, 17, "the crawl did not read every page, so it ran less than this test needs");
    assert.ok(site.maxInFlight() > 1, `the crawl never had two requests in flight, so this test measured nothing (${site.maxInFlight()})`);
    assert.ok(site.maxInFlight() <= MAX_IN_FLIGHT, `the crawl had ${site.maxInFlight()} requests in flight, more than ${MAX_IN_FLIGHT}`);
  } finally {
    await site.close();
    await rm(outDir, { recursive: true, force: true });
  }
});

test("a robots.txt group for CodeGoblinsCrawler, in any case, applies to the name the crawler gives", () => {
  // Arrange
  const robots = (body) => RobotsTxtFile.from("http://127.0.0.1:9/robots.txt", body);

  // Act
  const isNamedAllowed = robots("User-agent: CodeGoblinsCrawler\nDisallow: /\n").isAllowed("http://127.0.0.1:9/", USER_AGENT);
  const isLowerCaseAllowed = robots("User-agent: codegoblinscrawler\nDisallow: /\n").isAllowed("http://127.0.0.1:9/", USER_AGENT);
  const isOtherAllowed = robots("User-agent: SomeOtherBot\nDisallow: /\n").isAllowed("http://127.0.0.1:9/", USER_AGENT);

  // Assert
  assert.equal(isNamedAllowed, false);
  assert.equal(isLowerCaseAllowed, false);
  assert.equal(isOtherAllowed, true, "a group for another crawler kept this one out, so the test cannot tell a match from a refusal");
});
