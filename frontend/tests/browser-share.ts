import { readFileSync, writeFileSync } from "node:fs";
import type { JSONReport } from "@playwright/test/reporter";

// The browser job runs the board's browser tests as shares of one suite, one
// share to a job. Playwright's own --shard cuts its list of tests into runs of
// equal length, and that list puts every spec that launches a browser of its
// own, and then the whole stepped-in project, after the rest: the terminal,
// dictation and composer specs, which take about twice as long a test. So the
// last share held all of them and took 19 of its 20 minutes while another
// took 9 (#2119). Dealt by their number of tests instead, one share still
// took 18 minutes to the others' 13 to 15, since some specs take far longer a
// test than others. So this deals the spec files out by the seconds
// tests/browser-durations.json records for each, the longest first, each to
// the share with the fewest seconds so far, and every file goes to exactly
// one share. A spec the file does not list weighs its number of tests times
// the recorded files' seconds a test.
//
//   node tests/browser-share.ts <share>/<shares> <list.json> <test-list>
//
// reads the report of playwright test --list --reporter=json and writes the
// spec files of the share asked for, one to a line, for playwright test
// --test-list.

const DURATIONS = new URL("./browser-durations.json", import.meta.url);

// The part of a listed suite the dealing reads: a JSONReportSuite is one.
export interface ListedSuite {
  file: string;
  specs: { tests: unknown[] }[];
  suites?: ListedSuite[];
}

// The tests of suite, of every project.
export function testCount(suite: ListedSuite): number {
  const own = suite.specs.reduce((sum, spec) => sum + spec.tests.length, 0);
  return own + (suite.suites ?? []).reduce((sum, inner) => sum + testCount(inner), 0);
}

// The seconds each listed spec file is expected to take: what durations
// records for it, or its tests times the recorded files' seconds a test.
export function specWeights(suites: ListedSuite[], durations: Record<string, number>): Map<string, number> {
  const files = suites.map((suite) => ({ file: suite.file, tests: testCount(suite) }));
  const recorded = files.filter(({ file }) => Object.hasOwn(durations, file));
  const recordedTests = recorded.reduce((sum, { tests }) => sum + tests, 0);
  const perTest = recordedTests ? recorded.reduce((sum, { file }) => sum + durations[file], 0) / recordedTests : 1;
  return new Map(files.map(({ file, tests }) => [file, Object.hasOwn(durations, file) ? durations[file] : tests * perTest]));
}

// The spec files of each share, given the report's top-level suites, one to a
// spec file.
export function dealSpecFiles(suites: ListedSuite[], shares: number, durations: Record<string, number> = {}): string[][] {
  const weights = [...specWeights(suites, durations)].sort(([a, first], [b, second]) => second - first || a.localeCompare(b));
  const dealt = Array.from({ length: shares }, () => ({ files: [] as string[], seconds: 0 }));
  for (const [file, seconds] of weights) {
    const share = dealt.reduce((least, candidate) => (candidate.seconds < least.seconds ? candidate : least));
    share.files.push(file);
    share.seconds += seconds;
  }
  return dealt.map((share) => share.files.sort());
}

if (import.meta.main) {
  const [which, listFile, testListFile] = process.argv.slice(2);
  const match = /^([1-9]\d*)\/([1-9]\d*)$/.exec(which ?? "");
  if (!match || Number(match[1]) > Number(match[2]) || !listFile || !testListFile) {
    throw new Error(`usage: node tests/browser-share.ts <share>/<shares> <list.json> <test-list>, not ${process.argv.slice(2).join(" ")}`);
  }
  const report = JSON.parse(readFileSync(listFile, "utf8")) as JSONReport;
  if (report.errors.length) throw new Error(`the test list has errors: ${report.errors.map((error) => error.message).join("\n")}`);
  const durations = JSON.parse(readFileSync(DURATIONS, "utf8")) as Record<string, number>;
  const files = dealSpecFiles(report.suites, Number(match[2]), durations)[Number(match[1]) - 1];
  if (!files.length) throw new Error(`share ${which} has no spec files: there are fewer files than shares`);
  writeFileSync(testListFile, files.join("\n") + "\n");
  console.log(`share ${which} runs ${files.length} spec files: ${files.join(" ")}`);
}
