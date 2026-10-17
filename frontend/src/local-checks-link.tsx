import type { LocalChecks } from "./types";
import { Icon } from "./Icon";
import { localChecksLook } from "./local-checks";
import "./hosted-checks.css";

// A task's newest cfo gate test run beside its pull request: how it ended in
// a word or two, in its tone, with its level, time and failures in the tip,
// opening the end of the run's log. isIconOnly draws its mark alone and puts
// its words in the tip too.
export function LocalChecksLink({ checks, taskId, className, isIconOnly = false }: { checks: LocalChecks; taskId: string; className: string; isIconOnly?: boolean }) {
  const look = localChecksLook(checks, taskId);
  return <a className={className + " local-checks hosted-" + look.tone} href={look.url} target="_blank" rel="noreferrer" aria-label={look.text + ": " + look.tip} data-tip={isIconOnly ? look.text + ": " + look.tip : look.tip}>
    <Icon name={look.tone === "passed" ? "check" : "warning"} />{!isIconOnly && <span>{look.text}</span>}
  </a>;
}
