import { test } from "node:test";
import assert from "node:assert/strict";
import type { PlaywrightTestConfig } from "@playwright/test";

// The config reads CI when it is loaded, so a test sets it before importing,
// and asks for a copy of the module of its own, since one is read only once.
async function configWithCI(isCI: boolean) {
  const previous = process.env.CI;
  if (isCI) process.env.CI = "true";
  else delete process.env.CI;
  try {
    const url = new URL("../playwright.config.ts", import.meta.url);
    url.search = isCI ? "?ci=1" : "?ci=0";
    const { default: config } = (await import(url.href)) as { default: PlaywrightTestConfig };
    return config;
  } finally {
    if (previous === undefined) delete process.env.CI;
    else process.env.CI = previous;
  }
}

// A browser test that never finishes must fail its shard rather than hold the
// job to its own limit, with every merge train waiting on the run (#1406).
test("a CI browser run ends at a global timeout, and a desk run is not cut short", async () => {
  const ci = await configWithCI(true);
  const desk = await configWithCI(false);

  assert.ok(ci.globalTimeout, "a CI run has no globalTimeout, so a hung test runs until the job's limit");
  assert.ok(ci.globalTimeout >= 10 * 60_000 && ci.globalTimeout <= 60 * 60_000, `a CI run's globalTimeout is ${ci.globalTimeout} ms, want 10 to 60 minutes`);
  assert.equal(desk.globalTimeout, 0, "a run at a desk is bounded, where a whole suite on one worker may take longer than a shard");
});

// WebKit needs Media Foundation on a Windows runner, and that install waited
// 29 minutes on Windows Update on 2026-10-22 (#1406). So the specs that run
// in it are a project of their own, for the one shard that installs WebKit.
const IPHONE_SPECS = ["**/iphone-*.spec.ts"];

test("the iPhone specs are a WebKit project of their own", async () => {
  const config = await configWithCI(true);
  const projects = config.projects ?? [];
  const iphone = projects.find((project) => project.name === "iphone");
  const board = projects.find((project) => project.name === "board");

  assert.deepEqual(iphone?.testMatch, IPHONE_SPECS);
  assert.deepEqual(board?.testIgnore, IPHONE_SPECS, "the sharded Chromium run leaves the WebKit specs to the iPhone project");
});
