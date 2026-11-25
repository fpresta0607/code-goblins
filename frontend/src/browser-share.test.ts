import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { test } from "node:test";
import { dealSpecFiles, testCount, type ListedSuite } from "../tests/browser-share.ts";

const SOURCE = path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1"));
const FRONTEND = path.dirname(SOURCE);

function suite(file: string, tests: number, inner: ListedSuite[] = []): ListedSuite {
  return { file, specs: Array.from({ length: tests }, () => ({ tests: [{ projectName: "board" }] })), suites: inner };
}

// The tests of the files a share runs, of every project or of project alone.
function shareTests(suites: ListedSuite[], files: string[], project?: string): number {
  return suites.filter((listed) => files.includes(listed.file)).reduce((sum, listed) => sum + testCount(listed, project), 0);
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
// suite the browser job lists.
test("the board's suite is dealt into five even shares, the stepped-in project spread among them", () => {
  // Arrange
  const cli = path.join(FRONTEND, "node_modules", "@playwright", "test", "cli.js");
  const listing = execFileSync(process.execPath, [cli, "test", "--list", "--reporter=json", "--project=board", "--project=stepped-in"], { cwd: FRONTEND, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  const { suites } = JSON.parse(listing) as { suites: ListedSuite[] };
  const steppedInFiles = suites.filter((listed) => testCount(listed, "stepped-in") > 0);
  assert.ok(steppedInFiles.length > 1, "the stepped-in project lists fewer than two spec files");

  // Act
  const dealt = dealSpecFiles(suites, 5);

  // Assert
  const counts = dealt.map((files) => shareTests(suites, files));
  const largest = Math.max(...suites.map((listed) => testCount(listed)));
  assert.ok(Math.max(...counts) - Math.min(...counts) <= largest, `shares of ${counts.join(", ")} tests, the largest file ${largest}`);
  const steppedIn = dealt.map((files) => shareTests(suites, files, "stepped-in"));
  assert.ok(steppedIn.filter((count) => count > 0).length > 1, `the shares hold ${steppedIn.join(", ")} stepped-in tests`);
});
