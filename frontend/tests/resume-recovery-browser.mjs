import assert from "node:assert/strict";
import { argv, env, stdout } from "node:process";
import { chromium, expect } from "@playwright/test";
import { build } from "vite";

// The Go regression owns the scratch backend and invokes this with
// CFO_RESUME_BROWSER=1. Requests use its real lifecycle route and CLI.
const [origin, assets] = argv.slice(2);
assert.ok(origin && assets);
await build({ configFile: false, logLevel: "warn", build: { outDir: assets, emptyOutDir: false } });
const server = await chromium.launchServer({ headless: true });
stdout.write(`Headless browser pid ${server.process().pid}\n`);
try {
  const browser = await chromium.connect(server.wsEndpoint());
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  await context.route("**/*", (route) => route.request().url().startsWith(origin + "/") ? route.continue() : route.abort());
  await context.addInitScript(() => globalThis.localStorage.setItem("cfo-tour", "seen"));
  const page = await context.newPage();
  await page.goto(origin);
  await page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Board", exact: true }).click();
  const card = (id) => page.locator(".task-card-shell").filter({ has: page.getByRole("button", { name: "Stop " + id, exact: true }) });
  await expect(card("direct-task").locator(".card-status-text")).toHaveText("Paused by you");
  await page.getByRole("button", { name: "Resume direct-task", exact: true }).click();
  await expect(card("direct-task").locator(".card-status-text")).toHaveText("Working");
  await page.getByRole("button", { name: "Resume managed-task", exact: true }).click();
  await expect.poll(async () => {
    const response = await page.request.get(origin + "/api/snapshot");
    const snapshot = await response.json();
    return snapshot.tasks.find((task) => task.id === "managed-task")?.action_error;
  }).toContain("cfo pipeline migrate managed-task");
  await expect(card("managed-task").locator(".card-status-text")).toHaveText("Paused by you");
  await expect(page.getByRole("button", { name: "Resume managed-task", exact: true })).toBeEnabled();
  if (env.CFO_RESUME_SCREENSHOT) await page.screenshot({ path: env.CFO_RESUME_SCREENSHOT, fullPage: true });
  await context.close();
  await browser.close();
  stdout.write("Real board clicks: direct PR resumed; managed policy refusal remained paused with actionable CFO evidence.\n");
} finally {
  await server.close();
}
