import { readdirSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import type { FullConfig } from "@playwright/test";
import { build } from "vite";

const FIXTURES = "tests/fixtures";

// Builds the pages the tests load, before every run and into a folder of that
// run's own: the board itself and every page under tests/fixtures, bundled as
// the shipped board is. A run never reads a build older than its source, two
// runs at once never read each other's, and the folder goes when the run ends.
export default async function buildSite(config: FullConfig) {
  const root = path.dirname(config.configFile!);
  const site = await mkdtemp(path.join(os.tmpdir(), "board-test-site-"));
  process.env.BOARD_TEST_SITE = site;
  await build({
    root,
    configFile: false,
    logLevel: "warn",
    build: {
      outDir: site,
      emptyOutDir: true,
      rollupOptions: { input: ["index.html", ...readdirSync(path.join(root, FIXTURES)).filter((name) => name.endsWith(".html")).map((name) => FIXTURES + "/" + name)] },
    },
  });
  return () => rm(site, { recursive: true, force: true });
}
