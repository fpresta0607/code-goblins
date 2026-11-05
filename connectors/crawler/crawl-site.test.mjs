import assert from "node:assert/strict";
import childProcess from "node:child_process";
import { mkdtemp, readdir, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { after, before, describe, test } from "node:test";

import { RobotsTxtFile } from "@crawlee/utils";
import { chromium } from "playwright-core";

import { crawlSite, MAX_IN_FLIGHT, NAVIGATION_TIMEOUT_SECS, USER_AGENT } from "./crawl-site.mjs";
import { CARE_GUIDE, CARE_SHEET, PDF, SITEMAP_ARCHIVES, startFixtureSite } from "./fixture-site.mjs";

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
    assert.deepEqual(read, ["/", "/about", "/catalog", "/catalog/box-kite", "/gallery", "/live", "/shows", "/visit/", "/welcome", "/workshops"]);
    assert.deepEqual(route("/workshops").found_by, ["sitemap"]);
    assert.deepEqual(route("/catalog/box-kite").found_by, [new URL("/catalog", site.url).href]);
  });

  test("follows a link in a page's inline SVG, by href or by xlink:href", () => {
    const about = new URL("/about", site.url).href;
    assert.deepEqual(route("/shows")?.found_by, [about]);
    assert.deepEqual(route("/gallery")?.found_by, [about]);
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

  test("saves each file under a name of its own, where two addresses read alike", async () => {
    const guide = route("/docs/kites/care.pdf");
    const sheet = route("/docs/kites-care.pdf");
    assert.notEqual(guide.file, sheet.file);
    assert.deepEqual(await readFile(path.join(outDir, guide.file)), CARE_GUIDE);
    assert.deepEqual(await readFile(path.join(outDir, sheet.file)), CARE_SHEET);
  });

  test("reads a page another route redirects to once, as itself, and counts it once", async () => {
    const visit = new URL("/visit/", site.url).href;
    const saved = await Promise.all((await readdir(path.join(outDir, "pages"))).filter((name) => name.endsWith(".json")).map(async (name) => JSON.parse(await readFile(path.join(outDir, "pages", name), "utf8"))));
    assert.equal(route("/visit").status, "redirect");
    assert.equal(route("/visit").landed_on, visit);
    assert.equal(route("/visit/").status, "read");
    assert.equal(saved.filter((facts) => facts.landed_on === visit).length, 1, "the page /visit redirects to was saved more than once");
    assert.equal(report.counts.pages_read, saved.length);
  });

  test("reads a page that redirects to itself to set a cookie, and reports a redirect loop unread", () => {
    const routeAt = (address) => report.routes.find((entry) => entry.url === new URL(address, site.url).href);
    assert.ok(site.requests.filter((request) => request.path === "/welcome").length > 1, "/welcome was read at its first request, so it never redirected to itself");
    assert.equal(routeAt("/welcome").status, "read");
    assert.equal(routeAt("/loop").status, "redirect");
    assert.equal(routeAt("/loop?check=1").status, "unread");
    assert.equal(routeAt("/loop?check=1").reason, `it redirects in a loop, through ${new URL("/loop", site.url).href}`);
  });

  test("obeys robots.txt: a disallowed route is never asked for and is reported unread", () => {
    assert.ok(site.requests.some((request) => request.path === "/robots.txt"), "the crawl never read robots.txt");
    assert.ok(site.requests.some((request) => request.path === "/api/stock"), "the catalog's script never asked for its stock, so it asked for nothing a redirect sends into a disallowed path");
    assert.deepEqual(site.requests.map((request) => request.path).filter((pathname) => pathname.startsWith("/private/")), [], "the crawl asked for a route robots.txt disallows");
    assert.equal(route("/private/notes").status, "unread");
    assert.equal(route("/private/notes").reason, "robots.txt disallows it");
    assert.equal(route("/docs/old-price-list.pdf").status, "unread");
    assert.equal(route("/docs/old-price-list.pdf").reason, `it redirects to ${new URL("/private/price-list.pdf", site.url).href}, which robots.txt disallows`);
    assert.equal(route("/staff").status, "unread");
    assert.equal(route("/staff").reason, `it redirects to ${new URL("/private/staff-room", site.url).href}, which robots.txt disallows`);
    assert.equal(route("/staff").page, undefined);
  });

  test("lists every sitemap it did not read with why: past its limit of 50, or on another site", () => {
    const onSite = ["/sitemap.xml", "/sitemap-pages.xml", ...Array.from({ length: SITEMAP_ARCHIVES }, (_, index) => `/sitemap-archive-${index + 1}.xml`)].map((sitemap) => new URL(sitemap, site.url).href);
    const elsewhere = [new URL("/sitemap.xml", site.offSiteUrl).href, new URL("/sitemap.xml", site.otherHostUrl).href].sort();
    assert.deepEqual(report.sitemaps.map((sitemap) => sitemap.url).sort(), [...onSite, ...elsewhere].sort(), "a sitemap the site names is missing from the report");
    assert.equal(site.requests.filter((request) => request.path.startsWith("/sitemap")).length, 50, "the crawl did not ask the site for exactly its limit of 50 sitemaps");
    assert.equal(report.sitemaps.filter((sitemap) => sitemap.status === "unread" && sitemap.reason === "past the sitemap limit of 50").length, onSite.length - 50);
    assert.deepEqual(report.sitemaps.filter((sitemap) => sitemap.reason === "it is on another site").map((sitemap) => sitemap.url).sort(), elsewhere);
    assert.ok(!site.offSiteRequests.some((request) => request.path === "/sitemap.xml"), "the crawl asked another site for its sitemap");
  });

  test("holds a request to another site that redirects onto this one to robots.txt", () => {
    assert.ok(site.offSiteRequests.some((request) => request.path === "/widget.js"), "the catalog never asked the second site for its script, so nothing redirected onto the site");
    assert.deepEqual(site.requests.map((request) => request.path).filter((pathname) => pathname.startsWith("/private/")), [], "a redirect from the second site sent the crawl into a path robots.txt disallows");
  });

  test("reports why a page went unread when the site drops its connection, not only that the browser could not load it", () => {
    assert.equal(route("/kite-cam").status, "unread");
    assert.match(route("/kite-cam").reason, /socket hang up|ECONNRESET/);
  });

  test("opens no event stream or WebSocket to the site, which would hold a slot as long as its page is open", () => {
    assert.ok(site.requests.some((request) => request.path === "/api/stock"), "the catalog's script never ran, so it opened nothing");
    assert.deepEqual(site.requests.map((request) => request.path).filter((pathname) => pathname === "/events" || pathname === "/live/socket"), []);
  });

  test("follows a redirect of a script's POST as a GET without its body, as a browser does", () => {
    assert.deepEqual(site.requests.filter((request) => request.path === "/search").map((request) => request.method), ["POST"], "the catalog's script never posted its search");
    assert.deepEqual(site.requests.filter((request) => request.path === "/search/results").map((request) => request.method), ["GET"]);
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

test("a crawl keeps at most a few requests in flight at a time, the browser's own included, and a page that never settles holds up no page after it", async () => {
  // Arrange
  const extraPages = 12;
  const site = await startFixtureSite({ delayMs: 200, extraPages });
  const outDir = await mkdtemp(path.join(os.tmpdir(), "goblin-crawl-"));
  const asked = (pathname) => site.requests.filter((request) => request.path === pathname).length;

  try {
    // Act
    const report = await crawlSite({ startUrl: site.url, outDir });

    // Assert
    assert.ok(asked("/live/data") > MAX_IN_FLIGHT, "the live page never asked for more data than the crawl has slots, so none of its requests waited when it closed");
    assert.equal(report.counts.pages_read, 10 + extraPages, "the crawl did not read every page");
    assert.deepEqual(report.routes.filter((entry) => entry.status === "unread").map((entry) => new URL(entry.url)).map((url) => url.pathname + url.search).sort(), ["/docs/old-price-list.pdf", "/kite-cam", "/loop?check=1", "/private/notes", "/staff"]);
    for (let number = 1; number <= extraPages; number += 1) {
      assert.equal(asked(`/more/${number}`), 1, `/more/${number} was not read at its first navigation`);
    }
    const extraAt = site.requests.filter((request) => request.path.startsWith("/more/")).map((request) => request.at);
    const during = site.requests.map((request) => request.at).filter((at) => at >= Math.min(...extraAt) && at <= Math.max(...extraAt));
    const quietest = Math.max(...during.slice(1).map((at, index) => at - during[index]));
    assert.ok(quietest < (NAVIGATION_TIMEOUT_SECS * 1000) / 3, `the site heard nothing from the crawl for ${Math.round(quietest)} ms while it read the extra pages, near its navigation timeout of ${NAVIGATION_TIMEOUT_SECS} s`);
    assert.ok(asked("/assets/menu.js") >= extraPages, "the browser did not load each extra page's scripts, so this test measured only the crawler's own requests");
    assert.ok(site.maxInFlight() > 1, `the crawl never had two requests in flight, so this test measured nothing (${site.maxInFlight()})`);
    assert.ok(site.maxInFlight() <= MAX_IN_FLIGHT, `the crawl had ${site.maxInFlight()} requests in flight, more than ${MAX_IN_FLIGHT}`);
  } finally {
    await site.close();
    await rm(outDir, { recursive: true, force: true });
  }
});

test("the page limit counts pages read, not redirects, and reports every page past it unread", async () => {
  // Arrange
  const site = await startFixtureSite();
  const outDir = await mkdtemp(path.join(os.tmpdir(), "goblin-crawl-"));

  try {
    // Act
    const report = await crawlSite({ startUrl: new URL("/visit", site.url).href, outDir, maxPages: 1 });

    // Assert
    const routeOf = (pathname) => report.routes.find((entry) => new URL(entry.url).pathname === pathname);
    assert.equal(routeOf("/visit").status, "redirect", "the start route did not redirect, so the limit was never asked to leave a redirect out");
    assert.equal(report.counts.pages_read, 1);
    const past = report.routes.filter((entry) => entry.reason === "past the page limit of 1");
    assert.ok(past.length > 0, "no page was left past the limit, so the limit was never reached");
    assert.deepEqual(report.routes.filter((entry) => entry.kind === "page" && entry.status === "found").map((entry) => entry.url), [], "a page was left with no outcome");
  } finally {
    await site.close();
    await rm(outDir, { recursive: true, force: true });
  }
});

test("a page counts against the page limit once it is read, so one read again after its reading failed counts once", async () => {
  // Arrange
  const site = await startFixtureSite();
  const outDir = await mkdtemp(path.join(os.tmpdir(), "goblin-crawl-"));
  const browser = await chromium.launch({ channel: "chrome", headless: true });
  const pagePrototype = Object.getPrototypeOf(await browser.newPage());
  await browser.close();
  const { content } = pagePrototype;
  // Each page's HTML cannot be read the first time, as a page's cannot when
  // it navigates while it is read, such as one a meta refresh moves on.
  const failed = new Set();
  pagePrototype.content = async function () {
    if (failed.has(this.url())) return content.call(this);
    failed.add(this.url());
    throw new Error("page.content: Execution context was destroyed, most likely because of a navigation");
  };

  try {
    // Act
    const report = await crawlSite({ startUrl: site.url, outDir, maxPages: 1 });

    // Assert
    assert.ok(failed.size > 0, "no page's HTML was asked for, so no reading failed");
    assert.equal(report.counts.pages_read, 1, "a page whose reading failed counted against the limit, so fewer pages than the limit were read");
  } finally {
    pagePrototype.content = content;
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
