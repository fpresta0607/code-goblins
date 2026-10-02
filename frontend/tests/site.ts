import { existsSync } from "node:fs";
import path from "node:path";
import { test as base } from "@playwright/test";

export * from "@playwright/test";

// The folder vite.fixtures.config.ts builds the pages into.
const SITE = path.join(import.meta.dirname, "..", "test-site");

// Every test reads its pages from the built folder, answered by the test
// itself in place of a web server: no request opens a socket, so a connection
// the machine refuses cannot fail a page load. No supervisor runs behind the
// tests either, so an API request a test does not answer itself fails.
export const test = base.extend({
  context: async ({ context, baseURL }, use) => {
    await context.route(baseURL + "/**", (route) => {
      const { pathname } = new URL(route.request().url());
      const file = path.join(SITE, pathname === "/" ? "index.html" : decodeURIComponent(pathname));
      if (!pathname.startsWith("/api/") && existsSync(file)) return route.fulfill({ path: file });
      return route.fulfill({ status: pathname.startsWith("/api/") ? 502 : 404, body: "" });
    });
    await use(context);
  },
});
