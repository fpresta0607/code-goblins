import type { LocalChecks } from "./types";
import { Icon } from "./Icon";
import { localChecksLook } from "./local-checks";
import "./hosted-checks.css";

// A task's newest cfo gate test run beside its pull request: how it ended in
// a word or two, in its tone, with its level, time and failures in the tip,
// opening the end of the run's log.
export function LocalChecksLink({ checks, taskId, className }: { checks: LocalChecks; taskId: string; className: string }) {
  const look = localChecksLook(checks, taskId);
  return <a className={className + " local-checks hosted-" + look.tone} href={look.url} target="_blank" rel="noreferrer" aria-label={look.text + ": " + look.tip} data-tip={look.tip}>
    <Icon name={look.tone === "passed" ? "check" : "warning"} /><span>{look.text}</span>
  </a>;
}
