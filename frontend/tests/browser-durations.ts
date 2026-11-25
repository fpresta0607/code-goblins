import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

// Refreshes tests/browser-durations.json, the seconds each spec file took,
// which tests/browser-share.ts deals the browser jobs' shares out by, from
// the records each browser job of a go workflow run kept
// (tests/file-durations.ts):
//
//   node tests/browser-durations.ts <run-id>
//
// Take a run in which every browser job passed, so every spec file is in it:
// the file is written from that run alone.

const DURATIONS = new URL("./browser-durations.json", import.meta.url);
const RECORD = "browser-durations.json";

// Every share's record under folder, gathered into one: each spec file runs
// in exactly one share, so a file in two records is refused.
export function gatherDurations(folder: string): Record<string, number> {
  const records = readdirSync(folder, { recursive: true, encoding: "utf8" }).filter((name) => path.basename(name) === RECORD);
  if (!records.length) throw new Error(`no ${RECORD} under ${folder}: the run kept no browser job's durations`);
  const gathered: Record<string, number> = {};
  for (const record of records) {
    for (const [file, seconds] of Object.entries(JSON.parse(readFileSync(path.join(folder, record), "utf8")) as Record<string, number>)) {
      if (Object.hasOwn(gathered, file)) throw new Error(`${file} is in two shares' durations`);
      gathered[file] = seconds;
    }
  }
  return Object.fromEntries(Object.entries(gathered).sort(([a], [b]) => a.localeCompare(b)));
}

if (import.meta.main) {
  const run = process.argv[2];
  if (!run || !/^\d+$/.test(run)) throw new Error(`usage: node tests/browser-durations.ts <run-id>, not ${process.argv.slice(2).join(" ")}`);
  const folder = mkdtempSync(path.join(tmpdir(), "browser-durations-"));
  try {
    execFileSync("gh", ["run", "download", run, "--pattern", "browser-durations-*", "--dir", folder], { stdio: "inherit" });
    const durations = gatherDurations(folder);
    writeFileSync(DURATIONS, JSON.stringify(durations, null, 2) + "\n");
    console.log(`wrote the durations of ${Object.keys(durations).length} spec files from run ${run}`);
  } finally {
    rmSync(folder, { recursive: true, force: true });
  }
}
