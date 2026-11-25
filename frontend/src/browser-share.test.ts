import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import type { JSONReport, JSONReportSuite } from "@playwright/test/reporter";
import { dealSpecFiles, testCount, type ListedSuite } from "../tests/browser-share.ts";

const SOURCE = path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1"));
const FRONTEND = path.dirname(SOURCE);

function suite(file: string, tests: number, inner: ListedSuite[] = []): ListedSuite {
  return { file, specs: Array.from({ length: tests }, () => ({ tests: [{ projectName: "board" }] })), suites: inner };
}

// The tests of the files a share runs.
function shareTests(suites: ListedSuite[], files: string[]): number {
  return suites.filter((listed) => files.includes(listed.file)).reduce((sum, listed) => sum + testCount(listed), 0);
}

// The board's browser tests as playwright test --list --reporter=json lists
// them, of the files of testList alone when it is given.
function listBrowserTests(testList?: string): JSONReport {
  const cli = path.join(FRONTEND, "node_modules", "@playwright", "test", "cli.js");
  const only = testList === undefined ? [] : ["--test-list", testList];
  const listing = execFileSync(process.execPath, [cli, "test", "--list", "--reporter=json", "--project=board", "--project=stepped-in", ...only], { cwd: FRONTEND, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  return JSON.parse(listing) as JSONReport;
}

// Each test a listed suite holds, as its project, place and titles.
function listedTests(listed: JSONReportSuite, titles: string[] = []): string[] {
  const own = listed.specs.flatMap((spec) => spec.tests.map((each) => `${each.projectName} ${spec.file}:${spec.line}:${spec.column} ${[...titles, spec.title].join(" > ")}`));
  return [...own, ...(listed.suites ?? []).flatMap((inner) => listedTests(inner, [...titles, inner.title]))];
}

// The share the browser job deals, as the job runs tests/browser-share.ts.
function dealShare(which: string, listing: string, testList: string) {
  return spawnSync(process.execPath, [path.join(FRONTEND, "tests", "browser-share.ts"), which, listing, testList], { cwd: FRONTEND, encoding: "utf8" });
}

// Listed the way Playwright lists the board's suite: many small files, then
// the larger ones it puts after them, one with its tests in a describe block.
const LISTED = [
  ...Array.from({ length: 40 }, (_, index) => suite(`light-${String(index).padStart(2, "0")}.spec.ts`, 1 + (index % 7))),
  suite("terminal-input.spec.ts", 0, [suite("terminal-input.spec.ts", 62)]),
  suite("voice-bubble.spec.ts", 36),
  suite("composer.spec.ts", 38),
  suite("terminal-keys.spec.ts", 29),
];

for (const shares of [1, 2, 3, 5, 6]) {
  test(`every spec file runs in exactly one of ${shares} shares`, () => {
    // Act
    const dealt = dealSpecFiles(LISTED, shares);

    // Assert
    assert.equal(dealt.length, shares);
    assert.deepEqual(dealt.flat().sort(), LISTED.map((listed) => listed.file).sort());
  });
}

test("the shares' tests differ by no more than the largest file's", () => {
  // Act
  const counts = dealSpecFiles(LISTED, 5).map((files) => shareTests(LISTED, files));

  // Assert
  assert.equal(counts.reduce((sum, count) => sum + count, 0), shareTests(LISTED, LISTED.map((listed) => listed.file)));
  assert.ok(Math.max(...counts) - Math.min(...counts) <= 62, `shares of ${counts.join(", ")} tests`);
});

// Playwright's own --shard gave the last of five shares every spec it lists
// after the rest, the whole stepped-in project among them, and that share
// took 19 of its 20 minutes while another took 9 (#2119). This deals the
// suite the browser job lists, as the job does, and lists what Playwright
// would run of each share.
test("the board's suite is dealt into five even shares, each test in exactly one, the stepped-in project spread among them", () => {
  // Arrange
  const scratch = mkdtempSync(path.join(tmpdir(), "browser-share-"));
  try {
    const full = listBrowserTests();
    const listing = path.join(scratch, "browser-tests.json");
    writeFileSync(listing, JSON.stringify(full));
    const everyTest = full.suites.flatMap((listed) => listedTests(listed));
    assert.ok(everyTest.filter((listed) => listed.startsWith("stepped-in ")).length > 1, "the stepped-in project lists fewer than two tests");

    // Act
    const shares = [1, 2, 3, 4, 5].map((share) => {
      const testList = path.join(scratch, `browser-share-${share}.txt`);
      const dealt = dealShare(`${share}/5`, listing, testList);
      assert.equal(dealt.status, 0, dealt.stderr);
      return listBrowserTests(testList).suites.flatMap((listed) => listedTests(listed));
    });

    // Assert
    assert.deepEqual(shares.flat().sort(), [...everyTest].sort());
    const counts = shares.map((tests) => tests.length);
    const largest = Math.max(...full.suites.map((listed) => testCount(listed)));
    assert.ok(Math.max(...counts) - Math.min(...counts) <= largest, `shares of ${counts.join(", ")} tests, the largest file ${largest}`);
    const steppedIn = shares.map((tests) => tests.filter((listed) => listed.startsWith("stepped-in ")).length);
    assert.ok(steppedIn.filter((count) => count > 0).length > 1, `the shares hold ${steppedIn.join(", ")} stepped-in tests`);
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
});

for (const { problem, which, errors } of [
  { problem: "a listing with errors", which: "1/2", errors: [{ message: "broken.spec.ts: SyntaxError" }] },
  { problem: "a share of fewer spec files than shares", which: "3/3", errors: [] },
]) {
  test(`the browser job's share fails on ${problem}, and writes no test list`, () => {
    // Arrange
    const scratch = mkdtempSync(path.join(tmpdir(), "browser-share-"));
    try {
      const listing = path.join(scratch, "browser-tests.json");
      const testList = path.join(scratch, "browser-share.txt");
      writeFileSync(listing, JSON.stringify({ errors, suites: [suite("a.spec.ts", 3), suite("b.spec.ts", 2)] }));

      // Act
      const dealt = dealShare(which, listing, testList);

      // Assert
      assert.notEqual(dealt.status, 0, dealt.stdout);
      assert.equal(existsSync(testList), false);
    } finally {
      rmSync(scratch, { recursive: true, force: true });
    }
  });
}
