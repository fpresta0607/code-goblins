import { readFileSync, writeFileSync } from "node:fs";
import type { JSONReport } from "@playwright/test/reporter";

// The browser job runs the board's browser tests as shares of one suite, one
// share to a job. Playwright's own --shard cuts its list of tests into runs of
// equal length, and that list puts every spec that launches a browser of its
// own, and then the whole stepped-in project, after the rest: the terminal,
// dictation and composer specs, which take about twice as long a test. So the
// last share held all of them and took 19 of its 20 minutes while another
// took 9 (#2119). This deals the spec files out instead, the largest first,
// each to the share with the fewest tests so far, so every share gets some of
// each kind, and every file goes to exactly one share.
//
//   node tests/browser-share.ts <share>/<shares> <list.json> <test-list>
//
// reads the report of playwright test --list --reporter=json and writes the
// spec files of the share asked for, one to a line, for playwright test
// --test-list.

// The part of a listed suite the dealing reads: a JSONReportSuite is one.
export interface ListedSuite {
  file: string;
  specs: { tests: { projectName: string }[] }[];
  suites?: ListedSuite[];
}

// The tests of suite, of every project, or of project alone when it is named.
export function testCount(suite: ListedSuite, project?: string): number {
  const own = suite.specs.reduce((sum, spec) => sum + spec.tests.filter((listed) => project === undefined || listed.projectName === project).length, 0);
  return own + (suite.suites ?? []).reduce((sum, inner) => sum + testCount(inner, project), 0);
}

// The spec files of each share, given the report's top-level suites, one to a
// spec file.
export function dealSpecFiles(suites: ListedSuite[], shares: number): string[][] {
  const files = suites.map((suite) => ({ file: suite.file, tests: testCount(suite) }));
  files.sort((a, b) => b.tests - a.tests || a.file.localeCompare(b.file));
  const dealt = Array.from({ length: shares }, () => ({ files: [] as string[], tests: 0 }));
  for (const { file, tests } of files) {
    const share = dealt.reduce((fewest, candidate) => (candidate.tests < fewest.tests ? candidate : fewest));
    share.files.push(file);
    share.tests += tests;
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
  const files = dealSpecFiles(report.suites, Number(match[2]))[Number(match[1]) - 1];
  if (!files.length) throw new Error(`share ${which} has no spec files: there are fewer files than shares`);
  writeFileSync(testListFile, files.join("\n") + "\n");
  console.log(`share ${which} runs ${files.length} spec files: ${files.join(" ")}`);
}
