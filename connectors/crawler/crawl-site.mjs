import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { gunzipSync } from "node:zlib";

import { Configuration, EventManager, log, LogLevel, PlaywrightCrawler } from "@crawlee/playwright";
import { parseSitemap, RobotsTxtFile } from "@crawlee/utils";
import { chromium } from "playwright-core";

import packageJson from "./package.json" with { type: "json" };

// The crawler says who it is on every request, the browser's included, and
// where its owner reads what it is for, so a site's operator can tell it
// apart from a person and from a crawler that hides.
export const USER_AGENT = `CodeGoblinsCrawler/${packageJson.version} (+https://github.com/fpresta0607/code-goblins/blob/main/docs/crawler.md)`;

// At most this many requests to the site are in flight at a time, the
// browser's own for a page's scripts, styles and data included.
export const MAX_IN_FLIGHT = 3;

export const DEFAULT_MAX_PAGES = 500;

// A route whose path ends in one of these is a file to download, never a page
// to render.
const FILE_EXTENSIONS = new Set([
  "pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf", "csv", "txt", "epub",
  "zip", "gz", "7z", "rar", "tar",
  "png", "jpg", "jpeg", "gif", "webp", "svg", "avif", "ico",
  "mp3", "wav", "ogg", "mp4", "webm", "mov",
]);

// What a page loads that its content and its links never need: the crawl
// records each image's address and alt text from the page instead, and asks
// the site for less. An event stream never ends, so it would hold a slot for
// as long as its page is open.
const BLOCKED_RESOURCES = new Set(["image", "media", "font", "eventsource"]);

// The headers that describe a request's body, which a request turned into a
// GET by a redirect no longer has.
const BODY_HEADERS = new Set(["content-encoding", "content-language", "content-location", "content-type", "content-length"]);

// Crawlee scales its work to the machine's load, which it measures by listing
// every process on it, through a PowerShell it starts every second on
// Windows. The crawler keeps MAX_IN_FLIGHT requests in flight whatever the
// load instead, so it gives Crawlee an event manager that measures nothing,
// and a memory budget so it never asks the machine for its total.
const CRAWLEE_MEMORY_MBYTES = 4096;

export const NAVIGATION_TIMEOUT_SECS = 30;

const MAX_SITEMAPS = 50;
const MAX_REDIRECTS = 20;
const FETCH_TIMEOUT_MS = 30_000;
const SETTLE_TIMEOUT_MS = 5_000;

/**
 * Crawls the site at startUrl: every route its sitemaps name and its pages
 * link to, rendered in headless Chrome so a page a script draws is read as a
 * person sees it, and every file those pages link to, downloaded. It writes
 * each page's facts and HTML under outDir, the files under outDir/files, and
 * report.json and report.md, which name every route it could not read and
 * why. It obeys robots.txt, the browser's requests included, and keeps at
 * most MAX_IN_FLIGHT requests to the site in flight, as USER_AGENT.
 */
export async function crawlSite({ startUrl, outDir, maxPages = DEFAULT_MAX_PAGES }) {
  const start = new URL(startUrl);
  if (start.protocol !== "http:" && start.protocol !== "https:") {
    throw new Error(`${startUrl} is not an http or https address`);
  }
  start.hash = "";
  const origin = start.origin;
  await mkdir(path.join(outDir, "pages"), { recursive: true });
  await mkdir(path.join(outDir, "files"), { recursive: true });

  const robots = await readRobots(origin);
  const isAllowed = (url) => robots.file.isAllowed(url, USER_AGENT);
  const slots = slotsOf(MAX_IN_FLIGHT);
  const routes = new Map();
  const external = new Set();
  let pageCount = 0;

  const noteRoute = (url, foundBy) => {
    let route = routes.get(url);
    if (!route) {
      route = { url, found_by: [], kind: "page", status: "found", reason: "" };
      routes.set(url, route);
    }
    if (!route.found_by.includes(foundBy)) route.found_by.push(foundBy);
    return route;
  };

  // whyNotFollowed says why the crawl does not follow a redirect to target,
  // or "" when it does.
  const whyNotFollowed = (target) => {
    if (new URL(target).origin !== origin) return `it redirects off the site, to ${target}`;
    if (!isAllowed(target)) return `it redirects to ${target}, which robots.txt disallows`;
    return "";
  };

  // redirectsBackTo says whether the redirects the crawl recorded from target
  // lead back to url.
  const redirectsBackTo = (target, url) => {
    const seen = new Set();
    let at = target;
    while (at && !seen.has(at)) {
      if (at === url) return true;
      seen.add(at);
      const route = routes.get(at);
      at = route?.status === "redirect" ? route.landed_on : "";
    }
    return false;
  };

  const sitemaps = await readSitemaps(robots, origin, isAllowed);

  const crawler = new PlaywrightCrawler({
    log: log.child({ prefix: "goblin-crawl", level: LogLevel.ERROR }),
    minConcurrency: MAX_IN_FLIGHT,
    maxConcurrency: MAX_IN_FLIGHT,
    maxRequestRetries: 1,
    navigationTimeoutSecs: NAVIGATION_TIMEOUT_SECS,
    requestHandlerTimeoutSecs: 90,
    useSessionPool: false,
    retryOnBlocked: false,
    // One browser, a fresh context for each page: Crawlee's default, one
    // persistent context, launches a second Chrome only to read its version.
    launchContext: { launcher: chromium, useChrome: true, useIncognitoPages: true, userAgent: USER_AGENT, launchOptions: { headless: true } },
    // Playwright never routes a service worker's requests, so no page may
    // start one.
    browserPoolOptions: {
      useFingerprints: false,
      prePageCreateHooks: [
        (pageId, browserController, pageOptions) => {
          pageOptions.serviceWorkers = "block";
        },
      ],
    },
    // The crawler sends each request the browser makes to the site, from the
    // crawled page, a frame in it or a window it opens, itself: one redirect
    // step at a time, each step checked against robots.txt and holding one
    // slot while it is fetched. The browser follows no redirect of its own,
    // and a crawled page that redirects elsewhere is a redirect to the page
    // it names, queued as a link. Each page has a context of its own, so the
    // context's routes cover that page and nothing else, and no page opens a
    // WebSocket to the site, which no route sees.
    preNavigationHooks: [
      async ({ page, request: crawled }) => {
        const context = page.context();
        const fetchStep = async (route, hop) => {
          await slots.take();
          try {
            return page.isClosed() ? null : await route.fetch({ ...hop, maxRedirects: 0 });
          } finally {
            slots.release();
          }
        };
        await context.routeWebSocket((url) => url.host === start.host, (socket) => socket.close());
        await context.route("**/*", async (route) => {
          const request = route.request();
          if (BLOCKED_RESOURCES.has(request.resourceType())) return route.abort();
          if (new URL(request.url()).origin !== origin) return route.continue();
          if (!isAllowed(request.url())) return route.abort("blockedbyclient");
          const isCrawledPage = request.isNavigationRequest() && normalize(request.url()) === crawled.uniqueKey && request.frame() === page.mainFrame();
          let hop = { url: request.url() };
          let method = request.method();
          for (let redirects = 0; redirects <= MAX_REDIRECTS; redirects += 1) {
            let response;
            try {
              response = await fetchStep(route, hop);
            } catch {
              return route.abort("failed");
            }
            if (!response) return;
            const target = redirectTarget(response.status(), response.headers().location, hop.url);
            if (!target) return route.fulfill({ response });
            // A page that redirects to itself, as one that sets a cookie
            // first does, is followed here, where the cookie is kept.
            if (isCrawledPage && target !== crawled.uniqueKey) {
              await followRedirect(routes.get(crawled.uniqueKey), response.status(), target);
              return route.fulfill({ contentType: "text/html", body: "" });
            }
            if (!isAllowed(target)) return route.abort("blockedbyclient");
            if (isTurnedIntoGet(response.status(), method)) {
              method = "GET";
              hop = { method, postData: "", headers: withoutBodyHeaders(request.headers()) };
            }
            hop = { ...hop, url: target };
          }
          if (isCrawledPage) {
            const crawledRoute = routes.get(crawled.uniqueKey);
            crawledRoute.status = "unread";
            crawledRoute.reason = `it redirects more than ${MAX_REDIRECTS} times`;
            return route.fulfill({ contentType: "text/html", body: "" });
          }
          return route.abort("failed");
        });
      },
    ],
    requestHandler: async ({ request, page, response }) => {
      const route = routes.get(request.uniqueKey);
      if (route.status !== "found") return;
      const status = response?.status() ?? 0;
      route.status_code = status;
      const contentType = response?.headers()["content-type"] ?? "";
      if (status >= 400) {
        route.status = "unread";
        route.reason = `the site answered HTTP ${status}`;
        return;
      }
      if (contentType && !/html/i.test(contentType)) {
        route.kind = "file";
        route.status = "found";
        return;
      }
      const landed = new URL(page.url());
      landed.hash = "";
      const reason = whyNotFollowed(landed.href);
      if (reason) {
        route.status = "unread";
        route.reason = reason;
        return;
      }
      await page.waitForLoadState("networkidle", { timeout: SETTLE_TIMEOUT_MS }).catch(() => {});
      const facts = await page.evaluate(readPage);
      // The page limit counts pages read: a page past it stays queued, to be
      // reported unread, and the crawl starts no request once it is reached.
      if (pageCount >= maxPages) return;
      pageCount += 1;
      if (pageCount === maxPages) crawler.stop();
      // A route that redirects within the site is the page it lands on, read
      // once.
      let read = route;
      if (landed.href !== request.uniqueKey) {
        route.status = "redirect";
        route.landed_on = landed.href;
        read = noteRoute(landed.href, request.uniqueKey);
      }
      const name = savedName(pageCount, read.url);
      await writeFile(path.join(outDir, "pages", `${name}.html`), await page.content());
      await writeFile(path.join(outDir, "pages", `${name}.json`), JSON.stringify({ url: request.uniqueKey, landed_on: landed.href, status: status, ...facts }, null, 2));
      read.status = "read";
      read.page = `pages/${name}.json`;
      read.title = facts.title;
      await enqueue(facts.links, read.url);
    },
    errorHandler: async ({ request }, error) => {
      if (isDownload(error)) request.noRetry = true;
    },
    failedRequestHandler: async ({ request }, error) => {
      const route = routes.get(request.uniqueKey);
      if (route.status !== "found") return;
      if (isDownload(error)) {
        route.kind = "file";
        route.status = "found";
        return;
      }
      route.status = "unread";
      route.reason = firstLine(error?.message) || "the browser could not load it";
    },
  }, new Configuration({ persistStorage: false, purgeOnStart: true, eventManager: new EventManager(), memoryMbytes: CRAWLEE_MEMORY_MBYTES }));

  async function enqueue(links, foundBy) {
    const requests = [];
    for (const link of links) {
      const url = normalize(link);
      if (!url) continue;
      if (new URL(url).origin !== origin) {
        external.add(url);
        continue;
      }
      const route = noteRoute(url, foundBy);
      if (route.status !== "found" || route.queued) continue;
      if (!isAllowed(url)) {
        route.status = "unread";
        route.reason = "robots.txt disallows it";
        continue;
      }
      if (FILE_EXTENSIONS.has(extensionOf(url))) {
        route.kind = "file";
        continue;
      }
      route.queued = true;
      requests.push({ url, uniqueKey: url });
    }
    if (requests.length > 0) await crawler.addRequests(requests);
  }

  // followRedirect records that route, which answered status, redirects to
  // target. A redirect within the site is queued as a link the route found,
  // so the page it names is read once, by its own address.
  async function followRedirect(route, status, target) {
    route.status_code = status;
    const reason = whyNotFollowed(target) || (redirectsBackTo(target, route.url) ? `it redirects in a loop, through ${target}` : "");
    if (reason) {
      route.status = "unread";
      route.reason = reason;
      return;
    }
    route.status = "redirect";
    route.landed_on = target;
    await enqueue([target], route.url);
  }

  await enqueue([start.href], "start");
  await enqueue(sitemaps.pages, "sitemap");
  await crawler.run();

  for (const route of routes.values()) {
    if (route.kind === "page" && route.status === "found" && route.queued) {
      route.status = "unread";
      route.reason = `past the page limit of ${maxPages}`;
    }
    delete route.queued;
  }
  const files = [...routes.values()].filter((route) => route.kind === "file" && route.status === "found");
  await runPool(files, MAX_IN_FLIGHT, (route, index) => downloadFile(route, index + 1, outDir, isAllowed));

  const report = {
    start_url: start.href,
    crawled_at: new Date().toISOString(),
    user_agent: USER_AGENT,
    max_in_flight: MAX_IN_FLIGHT,
    max_pages: maxPages,
    robots_txt: robots.summary,
    sitemaps: sitemaps.read,
    routes: [...routes.values()].sort((a, b) => a.url.localeCompare(b.url)),
    external_links: [...external].sort(),
  };
  report.counts = {
    pages_read: report.routes.filter((route) => route.kind === "page" && route.status === "read").length,
    files_downloaded: report.routes.filter((route) => route.kind === "file" && route.status === "downloaded").length,
    unread: report.routes.filter((route) => route.status === "unread").length,
    external_links: report.external_links.length,
  };
  await writeFile(path.join(outDir, "report.json"), JSON.stringify(report, null, 2));
  await writeFile(path.join(outDir, "report.md"), reportMarkdown(report));
  return report;
}

// readPage runs in the page: what it says, how it is laid out and where it
// leads, as the rendered page holds it.
function readPage() {
  const textOf = (element) => (element.innerText || element.textContent || "").trim().replace(/\s+/g, " ");
  return {
    title: document.title,
    description: document.querySelector('meta[name="description"]')?.getAttribute("content") ?? "",
    language: document.documentElement.lang,
    headings: [...document.querySelectorAll("h1, h2, h3, h4, h5, h6")].map((heading) => ({ level: Number(heading.tagName[1]), text: textOf(heading) })),
    layout: [...document.querySelectorAll("header, nav, main, aside, footer, section, article, form")].map((element) => ({
      tag: element.tagName.toLowerCase(),
      id: element.id,
      classes: typeof element.className === "string" ? element.className : "",
      text: textOf(element).slice(0, 120),
    })),
    text: document.body ? document.body.innerText : "",
    tables: [...document.querySelectorAll("table")].map((table) => [...table.rows].map((row) => [...row.cells].map((cell) => textOf(cell)))),
    images: [...document.querySelectorAll("img")].map((image) => ({ src: image.src, alt: image.alt })),
    links: [...document.querySelectorAll("a[href], area[href]")].map((link) => link.href),
  };
}

// fetchAsCrawler asks for url as USER_AGENT and follows its redirects one at
// a time, never into a path isAllowed refuses.
async function fetchAsCrawler(url, isAllowed) {
  let current = url;
  for (let redirects = 0; redirects <= MAX_REDIRECTS; redirects += 1) {
    const response = await fetch(current, { headers: { "user-agent": USER_AGENT }, redirect: "manual", signal: AbortSignal.timeout(FETCH_TIMEOUT_MS) });
    const target = redirectTarget(response.status, response.headers.get("location"), current);
    if (!target) return response;
    await response.body?.cancel();
    if (!isAllowed(target)) throw new Error(`it redirects to ${target}, which robots.txt disallows`);
    current = target;
  }
  throw new Error(`it redirects more than ${MAX_REDIRECTS} times`);
}

// redirectTarget is where an answer of status, with location, sends a
// request for url, or "" when the answer is no redirect.
function redirectTarget(status, location, url) {
  if (status < 300 || status >= 400 || !location || !URL.canParse(location, url)) return "";
  const target = new URL(location, url);
  target.hash = "";
  return target.href;
}

// isTurnedIntoGet says whether a redirect of status turns a request of method
// into a GET without its body, as a browser's does: a POST answered 301 or
// 302, or anything but a GET or HEAD answered 303.
function isTurnedIntoGet(status, method) {
  return ((status === 301 || status === 302) && method === "POST") || (status === 303 && method !== "GET" && method !== "HEAD");
}

function withoutBodyHeaders(headers) {
  return Object.fromEntries(Object.entries(headers).filter(([name]) => !BODY_HEADERS.has(name.toLowerCase())));
}

// readRobots reads the site's robots.txt. A site with none, or one that
// answers 4xx for it, allows everything, as the robots.txt standard says; one
// that cannot answer is not crawled at all.
async function readRobots(origin) {
  const url = new URL("/robots.txt", origin).href;
  let response;
  try {
    response = await fetchAsCrawler(url, () => true);
  } catch (error) {
    throw new Error(`${url} could not be read (${firstLine(error.message)}), so nothing was crawled`);
  }
  if (response.status >= 500) {
    throw new Error(`${url} answered HTTP ${response.status}, so nothing was crawled`);
  }
  const content = response.ok ? await response.text() : "";
  return { file: RobotsTxtFile.from(url, content), summary: { url, status: response.status } };
}

// readSitemaps reads the sitemaps robots.txt names and /sitemap.xml, and every
// sitemap a sitemap index names, on the site only, and returns the pages they
// list and what each sitemap gave.
async function readSitemaps(robots, origin, isAllowed) {
  const queue = [...robots.file.getSitemaps(), new URL("/sitemap.xml", origin).href];
  const seen = new Set();
  const pages = [];
  const read = [];
  while (queue.length > 0 && seen.size < MAX_SITEMAPS) {
    const url = normalize(queue.shift());
    if (!url || seen.has(url) || new URL(url).origin !== origin) continue;
    seen.add(url);
    if (!isAllowed(url)) {
      read.push({ url, status: "unread", reason: "robots.txt disallows it" });
      continue;
    }
    let response;
    try {
      response = await fetchAsCrawler(url, isAllowed);
    } catch (error) {
      read.push({ url, status: "unread", reason: firstLine(error.message) });
      continue;
    }
    if (!response.ok) {
      read.push({ url, status: "unread", reason: `the site answered HTTP ${response.status}` });
      continue;
    }
    let content = Buffer.from(await response.arrayBuffer());
    if (content[0] === 0x1f && content[1] === 0x8b) content = gunzipSync(content);
    const text = content.toString("utf8");
    let listed = 0;
    if (/^\s*</.test(text)) {
      for await (const item of parseSitemap([{ type: "raw", content: text }], undefined, { emitNestedSitemaps: true, maxDepth: 0 })) {
        if (item.originSitemapUrl === null) queue.push(item.loc);
        else pages.push(item.loc);
        listed += 1;
      }
    } else {
      for (const line of text.split(/\r?\n/)) {
        if (/^https?:\/\//.test(line.trim())) {
          pages.push(line.trim());
          listed += 1;
        }
      }
    }
    read.push({ url, status: "read", listed });
  }
  return { pages, read };
}

async function downloadFile(route, number, outDir, isAllowed) {
  if (!isAllowed(route.url)) {
    route.status = "unread";
    route.reason = "robots.txt disallows it";
    return;
  }
  try {
    const response = await fetchAsCrawler(route.url, isAllowed);
    route.status_code = response.status;
    if (!response.ok) {
      route.status = "unread";
      route.reason = `the site answered HTTP ${response.status}`;
      return;
    }
    const body = Buffer.from(await response.arrayBuffer());
    const name = savedName(number, route.url);
    await writeFile(path.join(outDir, "files", name), body);
    route.status = "downloaded";
    route.file = `files/${name}`;
    route.bytes = body.length;
    route.content_type = response.headers.get("content-type") ?? "";
  } catch (error) {
    route.status = "unread";
    route.reason = firstLine(error.message);
  }
}

async function runPool(items, size, work) {
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const index = next;
      next += 1;
      await work(items[index], index);
    }
  };
  await Promise.all(Array.from({ length: Math.min(size, items.length) }, worker));
}

// slotsOf hands out at most size slots at a time, in the order they are
// asked for.
function slotsOf(size) {
  let free = size;
  const waiting = [];
  return {
    take: () => {
      if (free > 0) {
        free -= 1;
        return Promise.resolve();
      }
      return new Promise((resolve) => waiting.push(resolve));
    },
    release: () => {
      const next = waiting.shift();
      if (next) next();
      else free += 1;
    },
  };
}

function reportMarkdown(report) {
  const cell = (text) => String(text ?? "").replace(/\|/g, "\\|").replace(/\s+/g, " ");
  const lines = [
    `# Crawl of ${report.start_url}`,
    "",
    `Crawled at ${report.crawled_at} as \`${report.user_agent}\`, at most ${report.max_in_flight} requests at a time, obeying robots.txt (${report.robots_txt.url}, HTTP ${report.robots_txt.status}).`,
    "",
    `Pages read: ${report.counts.pages_read}. Files downloaded: ${report.counts.files_downloaded}. Routes unread: ${report.counts.unread}. Links off the site: ${report.counts.external_links}.`,
    "",
    "## Routes",
    "",
    "| Route | Kind | Status | Found by | Saved as |",
    "| --- | --- | --- | --- | --- |",
    ...report.routes.map((route) => `| ${cell(route.url)} | ${route.kind} | ${route.status}${route.status === "redirect" ? ` to ${cell(route.landed_on)}` : ""}${route.reason ? `: ${cell(route.reason)}` : ""} | ${cell(route.found_by.join(", "))} | ${cell(route.page ?? route.file ?? "")} |`),
    "",
    "## Unread routes",
    "",
  ];
  const unread = report.routes.filter((route) => route.status === "unread");
  lines.push(...(unread.length > 0 ? unread.map((route) => `- ${route.url}: ${route.reason}`) : ["None."]));
  lines.push("", "## Sitemaps", "");
  lines.push(...(report.sitemaps.length > 0 ? report.sitemaps.map((sitemap) => `- ${sitemap.url}: ${sitemap.status === "read" ? `entries listed: ${sitemap.listed}` : `unread, ${sitemap.reason}`}`) : ["None."]));
  lines.push("", "## Links off the site", "");
  lines.push(...(report.external_links.length > 0 ? report.external_links.map((url) => `- ${url}`) : ["None."]));
  return lines.join("\n") + "\n";
}

function normalize(link) {
  try {
    const url = new URL(link);
    if (url.protocol !== "http:" && url.protocol !== "https:") return "";
    url.hash = "";
    return url.href;
  } catch {
    return "";
  }
}

function extensionOf(url) {
  const name = new URL(url).pathname.split("/").pop() ?? "";
  const dot = name.lastIndexOf(".");
  return dot < 0 ? "" : name.slice(dot + 1).toLowerCase();
}

function slugOf(url) {
  const parsed = new URL(url);
  const slug = (parsed.pathname + parsed.search).replace(/^\/+|\/+$/g, "").replace(/[^A-Za-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
  return (slug || "index").slice(0, 80);
}

// savedName is the name a page or a file is saved under: its number in the
// crawl, which no other has, and its address, to read by.
function savedName(number, url) {
  return `${String(number).padStart(4, "0")}-${slugOf(url)}`;
}

function isDownload(error) {
  return /Download is starting|net::ERR_ABORTED/i.test(error?.message ?? "");
}

function firstLine(text) {
  return String(text ?? "").split("\n")[0].trim();
}
