import http from "node:http";
import { pathToFileURL } from "node:url";

// A made-up site for the crawler's tests and its live proof: a sitemap index,
// a page a script draws whose only link to a product page is drawn too, and
// whose script asks for data robots.txt disallows, directly and through a
// redirect, and starts a service worker that would ask for more, a page only
// the sitemap names, a page that redirects to one the sitemap names and one
// that redirects into a disallowed folder, a page whose script never stops
// asking for data, linked PDFs, two of them at addresses that differ only in
// a slash, one that redirects into a disallowed folder, and a folder
// robots.txt disallows. The catalog's script also posts a search a redirect
// answers, and opens an event stream, a WebSocket and a window into the
// disallowed folder. A page redirects to itself until it is asked with the
// cookie it sets, and two routes redirect to each other. It records every
// request it is sent, WebSocket handshakes included, with its method, its
// user agent and when it came, and the most requests it had in flight at once.

export const PDF = Buffer.from(
  "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n",
);
export const CARE_GUIDE = Buffer.concat([PDF, Buffer.from("% the kite care guide\n")]);
export const CARE_SHEET = Buffer.concat([PDF, Buffer.from("% the kite care sheet\n")]);

const page = (title, body) => `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>${title}</title><meta name="description" content="${title} at Fernwood Kites"></head><body>${body}</body></html>`;

const NAV = `<header><nav><a href="/">Home</a> <a href="/about">About</a> <a href="/catalog">Catalog</a></nav></header>`;

// What each extra page loads beside itself, as a page of a real site does.
const ASSETS = `<link rel="stylesheet" href="/assets/site.css"><script src="/assets/menu.js"></script><script src="/assets/cart.js"></script><script src="/assets/search.js"></script>`;

// What the live page's script does until the page is closed: several
// requests for its data at once, each sent again a moment after it is
// answered, so the page never settles.
const POLL = `for (let poller = 0; poller < 8; poller += 1) (async () => { for (;;) { await fetch("/live/data").then((answer) => answer.text()).catch(() => {}); await new Promise((resume) => setTimeout(resume, 20)); } })();`;

// Each of these answers with a redirect to where it names.
const REDIRECTS = {
  "/visit": "/visit/",
  "/staff": "/private/staff-room",
  "/api/stock": "/private/stock.json",
  "/search": "/search/results",
  "/docs/old-price-list.pdf": "/private/price-list.pdf",
};

/**
 * Starts the fixture site on a free local port. Every response waits delayMs.
 * extraPages adds that many pages, /more/1 and on, each linked from the home
 * page and loading a stylesheet and three scripts, for a crawl with more work
 * than it may do at once.
 */
export function startFixtureSite({ delayMs = 0, extraPages = 0 } = {}) {
  const requests = [];
  let inFlight = 0;
  let maxInFlight = 0;
  let origin = "";
  const extra = Array.from({ length: extraPages }, (_, index) => `/more/${index + 1}`);
  const routes = {
    "/robots.txt": () => ["text/plain", `User-agent: *\nDisallow: /private/\n\nSitemap: ${origin}/sitemap.xml\n`],
    "/sitemap.xml": () => ["application/xml", `<?xml version="1.0" encoding="UTF-8"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>${origin}/sitemap-pages.xml</loc></sitemap></sitemapindex>`],
    "/sitemap-pages.xml": () => ["application/xml", `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${["/", "/about", "/workshops", "/visit/"].map((route) => `<url><loc>${origin}${route}</loc></url>`).join("")}</urlset>`],
    "/": () => ["text/html", page("Fernwood Kites", `${NAV}<main><h1>Fernwood Kites</h1><p>Hand-sewn kites since 1987.</p><p><a href="/docs/price-list.pdf">Price list (PDF)</a> <a href="/docs/old-price-list.pdf">Last year's price list (PDF)</a> <a href="/docs/kites/care.pdf">Care guide (PDF)</a> <a href="/docs/kites-care.pdf">Care sheet (PDF)</a> <a href="/visit">Visit us</a> <a href="/staff">Staff room</a> <a href="/private/notes">Staff notes</a> <a href="/live">Live wind</a> <a href="/welcome">Welcome</a> <a href="/loop">Kite loop</a><a href="https://example.org/partner">Our partner</a></p>${extra.map((route) => `<a href="${route}">${route}</a>`).join(" ")}</main><footer>Fernwood Kites, a made-up shop</footer>`)],
    "/about": () => ["text/html", page("About", `${NAV}<main><h1>About us</h1><img src="/images/workshop.jpg" alt="The workshop"><table><tr><th>Day</th><th>Hours</th></tr><tr><td>Saturday</td><td>9 to 17</td></tr></table></main>`)],
    "/catalog": () => ["text/html", page("Catalog", `${NAV}<main id="catalog"><h1>Catalog</h1><p>Loading...</p></main><script src="/catalog.js"></script>`)],
    "/catalog.js": () => ["text/javascript", `document.getElementById("catalog").innerHTML = '<h1>Catalog</h1><ul><li><a href="/catalog/box-kite">Box kite</a></li></ul>'; fetch("/private/stock.json").catch(() => {}); fetch("/api/stock").catch(() => {}); Promise.resolve(navigator.serviceWorker?.register("/sw.js")).catch(() => {}); fetch("/search", { method: "POST", body: "q=box kite" }).catch(() => {}); new EventSource("/events"); new WebSocket(\`ws://\${location.host}/live/socket\`); window.open("/private/popup");`],
    "/sw.js": () => ["text/javascript", `self.addEventListener("install", (event) => event.waitUntil(fetch("/private/worker.json").catch(() => {})));`],
    "/live": () => ["text/html", page("Live wind", `${NAV}<main><h1>Live wind</h1><p>Wind at the kite field, as it changes.</p></main><script>${POLL}</script>`)],
    "/live/data": () => ["application/json", `{"wind_knots": 12}`],
    "/search/results": () => ["application/json", `{"kites": ["Box kite"]}`],
    "/events": () => ["text/event-stream", "data: calm\n\n"],
    "/welcome": () => ["text/html", page("Welcome", `${NAV}<main><h1>Welcome</h1><p>Read once the shop's cookie is set.</p></main>`)],
    "/catalog/box-kite": () => ["text/html", page("Box kite", `${NAV}<main><h1>Box kite</h1><p>A kite drawn only by the catalog's script.</p></main>`)],
    "/workshops": () => ["text/html", page("Workshops", `${NAV}<main><h1>Workshops</h1><p>Only the sitemap links here.</p></main>`)],
    "/visit/": () => ["text/html", page("Visit us", `${NAV}<main><h1>Visit us</h1><p>The shop is open on Saturdays.</p></main>`)],
    "/private/notes": () => ["text/html", page("Staff notes", "<p>robots.txt disallows this page.</p>")],
    "/private/staff-room": () => ["text/html", page("Staff room", "<p>robots.txt disallows this page.</p>")],
    "/private/price-list.pdf": () => ["application/pdf", PDF],
    "/docs/price-list.pdf": () => ["application/pdf", PDF],
    "/docs/kites/care.pdf": () => ["application/pdf", CARE_GUIDE],
    "/docs/kites-care.pdf": () => ["application/pdf", CARE_SHEET],
    "/assets/site.css": () => ["text/css", "main { margin: 1rem; }"],
    "/assets/menu.js": () => ["text/javascript", "window.menu = true;"],
    "/assets/cart.js": () => ["text/javascript", "window.cart = true;"],
    "/assets/search.js": () => ["text/javascript", "window.search = true;"],
  };
  for (const route of extra) routes[route] = () => ["text/html", page(route, `${NAV}<main><h1>${route}</h1></main>${ASSETS}`)];

  const record = (request) => {
    const url = new URL(request.url, origin);
    requests.push({ path: url.pathname, method: request.method, userAgent: request.headers["user-agent"] ?? "", at: performance.now() });
    return url;
  };
  const server = http.createServer((request, response) => {
    const { pathname, search } = record(request);
    inFlight += 1;
    maxInFlight = Math.max(maxInFlight, inFlight);
    response.on("close", () => {
      inFlight -= 1;
    });
    setTimeout(() => {
      if (REDIRECTS[pathname]) {
        response.writeHead(302, { location: REDIRECTS[pathname] }).end();
        return;
      }
      if (pathname === "/welcome" && !/(^|;\s*)visited=1/.test(request.headers.cookie ?? "")) {
        response.writeHead(302, { location: "/welcome", "set-cookie": "visited=1; Path=/" }).end();
        return;
      }
      if (pathname === "/loop") {
        response.writeHead(302, { location: search ? "/loop" : "/loop?check=1" }).end();
        return;
      }
      const route = routes[pathname];
      if (!route) {
        response.writeHead(404, { "content-type": "text/plain" }).end("not found");
        return;
      }
      const [contentType, body] = route();
      response.writeHead(200, { "content-type": contentType }).end(body);
    }, delayMs);
  });
  server.on("upgrade", (request, socket) => {
    record(request);
    socket.destroy();
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => {
      origin = `http://127.0.0.1:${server.address().port}`;
      resolve({
        url: `${origin}/`,
        requests,
        maxInFlight: () => maxInFlight,
        close: () => new Promise((done) => {
          server.closeAllConnections();
          server.close(done);
        }),
      });
    });
  });
}

// Run on its own, it serves the site for the live proof until the time it is
// given runs out.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const seconds = Number(process.argv[2] ?? 300);
  const site = await startFixtureSite();
  process.stdout.write(`fixture site: ${site.url} for ${seconds} s\n`);
  await new Promise((resolve) => setTimeout(resolve, seconds * 1000));
  await site.close();
}
