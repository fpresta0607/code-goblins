import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { createServer } from "vite";

// The dev server denies the files its server.fs.deny rule names, a .env
// holding a key among them. A request for one as a wasm instance read the file
// from disk anyway and answered with the parse error's first four bytes, and
// Vite 8.3.0 to 8.3.2 did so on every machine (GHSA-vfpm-58rq-9qcg, #1417).
test("the dev server refuses a denied file asked for as a wasm instance", async () => {
  // Arrange
  const root = mkdtempSync(path.join(tmpdir(), "board-wasm-"));
  writeFileSync(path.join(root, "index.html"), "<!doctype html>\n");
  // The file starts with "S", which the error shows as 53, so an answer that
  // read it names that byte whichever way it is quoted.
  writeFileSync(path.join(root, ".env"), "SECRET_KEY=abc123\n");
  const server = await createServer({ root, configFile: false, server: { host: "127.0.0.1", port: 0 }, logLevel: "silent" });
  try {
    await server.listen();
    const address = server.httpServer?.address();
    assert.ok(address && typeof address === "object", "the dev server listens on a port");
    const origin = "http://127.0.0.1:" + address.port;

    // Act
    const refused = await fetch(origin + "/.env");
    const probed = await fetch(origin + "/.env?vite-wasm-instance");
    const answer = await probed.text();

    // Assert
    assert.equal(refused.status, 403, "the dev server denies the file when it is asked for plainly");
    assert.equal(probed.status, 403, `the wasm instance request answered ${probed.status}: ${answer.slice(0, 300)}`);
    assert.ok(!answer.includes("53 45 43 52") && !answer.includes("SECRET_KEY"), `the wasm instance answer holds the denied file: ${answer.slice(0, 300)}`);
  } finally {
    await server.close();
    rmSync(root, { recursive: true, force: true });
  }
});
