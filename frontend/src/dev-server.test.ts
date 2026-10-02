import { test } from "node:test";
import assert from "node:assert/strict";
import path from "node:path";
import { createServer } from "vite";

// The browser tests read a build, so this is the one test of development mode:
// the dev server starts with the project's own configuration, on a port the
// machine picks, and serves the board's page and its entry module compiled.
test("the dev server starts and serves the board's page and its entry module", async () => {
  const server = await createServer({ configFile: path.join(import.meta.dirname, "..", "vite.config.ts"), server: { port: 0, strictPort: false }, logLevel: "silent" });
  try {
    await server.listen();
    const address = server.httpServer?.address();
    assert.ok(address && typeof address === "object", "the dev server listens on a port");
    const origin = "http://127.0.0.1:" + address.port;

    const page = await fetch(origin + "/");
    assert.equal(page.status, 200);
    assert.match(await page.text(), /<script type="module" src="\/src\/main\.tsx"><\/script>/);

    const entry = await fetch(origin + "/src/main.tsx");
    assert.equal(entry.status, 200);
    assert.match(entry.headers.get("content-type") || "", /javascript/);
    // Compiled for the browser: the JSX is gone and its imports are resolved.
    const code = await entry.text();
    assert.match(code, /createRoot/);
    assert.doesNotMatch(code, /from "react-dom\/client"/);
  } finally {
    await server.close();
  }
});
