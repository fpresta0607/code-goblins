import path from "node:path";
import type { FullConfig } from "@playwright/test";
import { build } from "vite";

// Builds the pages the tests load before every run, so a run never reads a
// build older than the source.
export default async function buildSite(config: FullConfig) {
  await build({ configFile: path.join(path.dirname(config.configFile!), "vite.fixtures.config.ts"), logLevel: "warn" });
}
