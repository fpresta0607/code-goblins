import { appendFileSync } from "node:fs";
import type { Reporter, TestCase, TestResult } from "@playwright/test/reporter";

// In CI a browser test that fails runs once more, and one that passes then
// leaves the run green. This reporter keeps such a test from passing unseen:
// it says so in the job's log and appends the test to the file
// FAILED_ONCE_RECORD names, one JSON object to a line, with the job
// FAILED_ONCE_JOB names, the spec file and the test's title. The go jobs
// write the same shape (tools/citest), and the workflow's test job names
// every line on the check a pull request requires.
export default class FailedOnce implements Reporter {
  onTestEnd(test: TestCase, result: TestResult) {
    if (result.status !== "passed" || test.outcome() !== "flaky") return;
    const [, , suite, ...title] = test.titlePath();
    const name = title.join(" > ");
    console.log(`failed once and passed on the second try: ${suite} ${name}`);
    const record = process.env.FAILED_ONCE_RECORD;
    if (record) appendFileSync(record, JSON.stringify({ job: process.env.FAILED_ONCE_JOB ?? "", suite, test: name }) + "\n");
  }

  printsToStdio() {
    return false;
  }
}
