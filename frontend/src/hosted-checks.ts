import { safeGitHubLink } from "./people.ts";
import type { HostedChecks } from "./types";

// HostedChecksLook is how a pull request's hosted checks show beside it: the
// words, a tip with the detail, a tone, and the page a click opens.
export interface HostedChecksLook { text: string; tip: string; tone: "pending" | "failed" | "cancelled" | "passed"; url: string }

// hostedChecksLook says where a pull request's hosted checks stand in plain
// words. Checks still running read Testing, as a pull request on a merge
// train does, and open the run that tests it. A failure opens the first
// failed check's own page, so its log is one click away. Either opens the
// pull request's checks when that page is not on GitHub, and so does every
// other state. Nothing is opened that is not on GitHub.
export function hostedChecksLook(checks: HostedChecks, pr: string): HostedChecksLook {
  const pullRequest = safeGitHubLink(pr);
  const checksPage = pullRequest ? pullRequest + "/checks" : "";
  const counted = checks.checks === 1 ? "1 check" : checks.checks + " checks";
  switch (checks.state) {
    case "failed":
      return { text: "Checks failed", tip: "Failed: " + checks.failed.join(", "), tone: "failed", url: safeGitHubLink(checks.link) || checksPage };
    case "cancelled":
      return { text: "Checks cancelled", tip: "Cancelled: " + checks.failed.join(", "), tone: "cancelled", url: safeGitHubLink(checks.link) || checksPage };
    case "passed":
      return { text: "Checks passed", tip: "All " + counted + " passed", tone: "passed", url: checksPage };
  }
  return { text: "Testing", tip: counted + ", still running", tone: "pending", url: safeGitHubLink(checks.link) || checksPage };
}
