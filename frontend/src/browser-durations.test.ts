import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import { gatherDurations } from "../tests/browser-durations.ts";
import FileDurations from "../tests/file-durations.ts";

function inScratch(body: (scratch: string) => void) {
  const scratch = mkdtempSync(path.join(tmpdir(), "browser-durations-"));
  try {
    body(scratch);
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

test("a browser job records the seconds each spec file's tests took, every try counted", () => {
  inScratch((scratch) => {
    // Arrange
    const record = path.join(scratch, "browser-durations.json");
    const testFolder = path.join(scratch, "tests");
    const reporter = new FileDurations();
    const before = process.env.BROWSER_DURATIONS_RECORD;
    process.env.BROWSER_DURATIONS_RECORD = record;
    try {
      // Act
      reporter.onBegin({ rootDir: testFolder });
      reporter.onTestEnd({ location: { file: path.join(testFolder, "slow.spec.ts") } }, { duration: 1500 });
      reporter.onTestEnd({ location: { file: path.join(testFolder, "slow.spec.ts") } }, { duration: 2000 });
      reporter.onTestEnd({ location: { file: path.join(testFolder, "fixtures", "a.spec.ts") } }, { duration: 250 });
      reporter.onEnd();
    } finally {
      if (before === undefined) delete process.env.BROWSER_DURATIONS_RECORD;
      else process.env.BROWSER_DURATIONS_RECORD = before;
    }

    // Assert
    assert.deepEqual(JSON.parse(readFileSync(record, "utf8")), { "fixtures/a.spec.ts": 0.3, "slow.spec.ts": 3.5 });
  });
});

test("a run's durations are every share's record gathered into one", () => {
  inScratch((scratch) => {
    // Arrange
    for (const [share, durations] of [["browser-durations-1", { "b.spec.ts": 20 }], ["browser-durations-2", { "a.spec.ts": 10, "c.spec.ts": 5 }]] as const) {
      mkdirSync(path.join(scratch, share));
      writeFileSync(path.join(scratch, share, "browser-durations.json"), JSON.stringify(durations));
    }

    // Act
    const gathered = gatherDurations(scratch);

    // Assert
    assert.deepEqual(Object.entries(gathered), [["a.spec.ts", 10], ["b.spec.ts", 20], ["c.spec.ts", 5]]);
  });
});

for (const { problem, records } of [
  { problem: "no share's record", records: [] },
  { problem: "a spec file in two shares' records", records: [{ "a.spec.ts": 10 }, { "a.spec.ts": 12 }] },
]) {
  test(`a run's durations are refused with ${problem}`, () => {
    inScratch((scratch) => {
      // Arrange
      records.forEach((durations, index) => {
        mkdirSync(path.join(scratch, `browser-durations-${index + 1}`));
        writeFileSync(path.join(scratch, `browser-durations-${index + 1}`, "browser-durations.json"), JSON.stringify(durations));
      });

      // Act and Assert
      assert.throws(() => gatherDurations(scratch));
    });
  });
}
