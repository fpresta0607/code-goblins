import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import type { JSONReport, JSONReportSuite } from "@playwright/test/reporter";
import { dealSpecFiles, specWeights, testCount, type ListedSuite } from "../tests/browser-share.ts";

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

test("a spec weighs the seconds the durations record, and one they do not record its tests times their seconds a test", () => {
  // Arrange
  const listed = [suite("slow.spec.ts", 10), suite("quick.spec.ts", 10), suite("new.spec.ts", 5)];

  // Act
  const weights = specWeights(listed, { "slow.spec.ts": 100, "quick.spec.ts": 20, "gone.spec.ts": 500 });

  // Assert
  assert.deepEqual([...weights], [["slow.spec.ts", 100], ["quick.spec.ts", 20], ["new.spec.ts", 30]]);
});

test("with no durations recorded, a spec weighs its number of tests", () => {
  assert.deepEqual([...specWeights(LISTED.slice(0, 3), {})], LISTED.slice(0, 3).map((listed) => [listed.file, testCount(listed)]));
});

// Dealt by their number of tests, the shares took 13 to 15 minutes but one
// 18 of its 20 (#2119): some specs take far longer a test than others.
test("a spec that takes long is dealt apart from quicker ones with more tests", () => {
  // Arrange
  const listed = [suite("slow.spec.ts", 2), suite("first.spec.ts", 10), suite("second.spec.ts", 10)];

  // Act
  const dealt = dealSpecFiles(listed, 2, { "slow.spec.ts": 300, "first.spec.ts": 150, "second.spec.ts": 150 });

  // Assert
  assert.deepEqual(dealt, [["slow.spec.ts"], ["first.spec.ts", "second.spec.ts"]]);
});

// Playwright's own --shard gave the last of five shares every spec it lists
// after the rest, the whole stepped-in project among them, and that share
// took 19 of its 20 minutes while another took 9 (#2119). This deals the
// suite the browser job lists, by the durations it deals by, as the job
// does, and lists what Playwright would run of each share.
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
    const weights = specWeights(full.suites, JSON.parse(readFileSync(path.join(FRONTEND, "tests", "browser-durations.json"), "utf8")) as Record<string, number>);
    const seconds = shares.map((tests) => [...new Set(tests.map((listed) => listed.split(" ")[1].replace(/:\d+:\d+$/, "")))].reduce((sum, file) => sum + (weights.get(file) ?? 0), 0));
    const largest = Math.max(...weights.values());
    assert.ok(Math.max(...seconds) - Math.min(...seconds) <= largest, `shares of ${seconds.map(Math.round).join(", ")} s, the longest file ${Math.round(largest)} s`);
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
