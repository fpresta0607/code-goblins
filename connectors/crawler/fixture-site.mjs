import http from "node:http";
import { pathToFileURL } from "node:url";

// A made-up site for the crawler's tests and its live proof: a sitemap index,
// a page a script draws whose only link to a product page is drawn too, a
// page only the sitemap names, a linked PDF, and a folder robots.txt
// disallows. It records every request it is sent, with its user agent, and
// the most requests it had in flight at once.

export const PDF = Buffer.from(
  "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n",
);

const page = (title, body) => `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>${title}</title><meta name="description" content="${title} at Fernwood Kites"></head><body>${body}</body></html>`;

const NAV = `<header><nav><a href="/">Home</a> <a href="/about">About</a> <a href="/catalog">Catalog</a></nav></header>`;

/**
 * Starts the fixture site on a free local port. Every response waits delayMs.
 * extraPages adds that many pages, /more/1 and on, each linked from the home
 * page, for a crawl with more work than it may do at once.
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
    "/sitemap-pages.xml": () => ["application/xml", `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${["/", "/about", "/workshops"].map((route) => `<url><loc>${origin}${route}</loc></url>`).join("")}</urlset>`],
    "/": () => ["text/html", page("Fernwood Kites", `${NAV}<main><h1>Fernwood Kites</h1><p>Hand-sewn kites since 1987.</p><p><a href="/docs/price-list.pdf">Price list (PDF)</a> <a href="/private/notes">Staff notes</a> <a href="https://example.org/partner">Our partner</a></p>${extra.map((route) => `<a href="${route}">${route}</a>`).join(" ")}</main><footer>Fernwood Kites, a made-up shop</footer>`)],
    "/about": () => ["text/html", page("About", `${NAV}<main><h1>About us</h1><img src="/images/workshop.jpg" alt="The workshop"><table><tr><th>Day</th><th>Hours</th></tr><tr><td>Saturday</td><td>9 to 17</td></tr></table></main>`)],
    "/catalog": () => ["text/html", page("Catalog", `${NAV}<main id="catalog"><h1>Catalog</h1><p>Loading...</p></main><script src="/catalog.js"></script>`)],
    "/catalog.js": () => ["text/javascript", `document.getElementById("catalog").innerHTML = '<h1>Catalog</h1><ul><li><a href="/catalog/box-kite">Box kite</a></li></ul>';`],
    "/catalog/box-kite": () => ["text/html", page("Box kite", `${NAV}<main><h1>Box kite</h1><p>A kite drawn only by the catalog's script.</p></main>`)],
    "/workshops": () => ["text/html", page("Workshops", `${NAV}<main><h1>Workshops</h1><p>Only the sitemap links here.</p></main>`)],
    "/private/notes": () => ["text/html", page("Staff notes", "<p>robots.txt disallows this page.</p>")],
    "/docs/price-list.pdf": () => ["application/pdf", PDF],
  };
  for (const route of extra) routes[route] = () => ["text/html", page(route, `${NAV}<main><h1>${route}</h1></main>`)];

  const server = http.createServer((request, response) => {
    const pathname = new URL(request.url, origin).pathname;
    requests.push({ path: pathname, userAgent: request.headers["user-agent"] ?? "" });
    inFlight += 1;
    maxInFlight = Math.max(maxInFlight, inFlight);
    response.on("close", () => {
      inFlight -= 1;
    });
    setTimeout(() => {
      const route = routes[pathname];
      if (!route) {
        response.writeHead(404, { "content-type": "text/plain" }).end("not found");
        return;
      }
      const [contentType, body] = route();
      response.writeHead(200, { "content-type": contentType }).end(body);
    }, delayMs);
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
