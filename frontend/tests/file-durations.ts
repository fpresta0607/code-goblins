import { writeFileSync } from "node:fs";
import path from "node:path";
import type { Reporter } from "@playwright/test/reporter";

// Records how long each spec file's tests took, every try counted, as
// seconds by the file's path from the test folder, in the JSON file
// BROWSER_DURATIONS_RECORD names. Each browser job keeps its record, and
// node tests/browser-durations.ts <run> gathers a run's records into
// tests/browser-durations.json, which tests/browser-share.ts deals the spec
// files out by.
export default class FileDurations implements Reporter {
  private testFolder = "";
  private readonly seconds = new Map<string, number>();

  onBegin(config: { rootDir: string }) {
    this.testFolder = config.rootDir;
  }

  onTestEnd(test: { location: { file: string } }, result: { duration: number }) {
    const file = path.relative(this.testFolder, test.location.file).replaceAll("\\", "/");
    this.seconds.set(file, (this.seconds.get(file) ?? 0) + result.duration / 1000);
  }

  onEnd() {
    const record = process.env.BROWSER_DURATIONS_RECORD;
    if (!record) return;
    const sorted = [...this.seconds].sort(([a], [b]) => a.localeCompare(b));
    writeFileSync(record, JSON.stringify(Object.fromEntries(sorted.map(([file, seconds]) => [file, Math.round(seconds * 10) / 10])), null, 2) + "\n");
  }

  printsToStdio() {
    return false;
  }
}
