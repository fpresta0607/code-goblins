import { existsSync } from "node:fs";
import path from "node:path";
import { test as base, type BrowserContext } from "@playwright/test";

export * from "@playwright/test";

// The origin the pages are given. Its name is reserved for tests and resolves
// nowhere, so a request no test answers fails at once: it cannot reach
// whatever happens to listen on a port of this machine.
export const ORIGIN = "https://board.test";

// servePages answers a browser context's requests for the board's pages from
// the folder tests/build-site.ts built them into, in place of a web server. No
// request opens a socket, so a connection the machine refuses cannot fail a
// page load. No supervisor runs behind the tests either, so an API request a
// test does not answer itself fails.
export async function servePages(context: BrowserContext): Promise<void> {
  const site = process.env.BOARD_TEST_SITE!;
  await context.route(ORIGIN + "/**", (route) => {
    const { pathname } = new URL(route.request().url());
    const file = path.join(site, pathname === "/" ? "index.html" : decodeURIComponent(pathname));
    if (!pathname.startsWith("/api/") && existsSync(file)) return route.fulfill({ path: file });
    return route.fulfill({ status: pathname.startsWith("/api/") ? 502 : 404, body: "" });
  });
}

// A test's own context has its pages served; a test that opens another
// context serves that one itself.
export const test = base.extend({
  context: async ({ context }, use) => {
    await servePages(context);
    await use(context);
  },
});
