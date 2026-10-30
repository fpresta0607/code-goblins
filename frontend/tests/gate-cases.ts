import { relative } from "node:path";
import type { Reporter, TestCase } from "@playwright/test/reporter";

// cfo gate prepush runs the browser specs a change picks with this reporter
// beside the list reporter, and reads its rows as the check runs, so a check
// the time limit ends still names a case that failed before it
// (internal/gatetest/browser.go). The list reporter marks a row by the raw
// status, so a case declared with test.fail that fails as expected gets the
// same x as a case that failed (#1587). This one weighs each try against the
// status the case expects, and writes a row when the case is "unexpected" so
// far or "flaky", a retry having passed it, as each try ends.
export default class GateCases implements Reporter {
  onTestEnd(test: TestCase) {
    const outcome = test.outcome();
    if (outcome !== "unexpected" && outcome !== "flaky") return;
    const [, project, , ...titles] = test.titlePath();
    const file = relative(process.cwd(), test.location.file).replaceAll("\\", "/");
    process.stdout.write(`cfo gate case: ${outcome} [${project}] ${file}:${test.location.line}:${test.location.column} › ${titles.join(" › ")}\n`);
  }

  printsToStdio() {
    return false;
  }
}
