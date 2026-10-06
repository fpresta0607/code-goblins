import type { HostedChecks } from "./types";
import { Icon, type IconName } from "./Icon";
import { hostedChecksLook } from "./hosted-checks";
import "./hosted-checks.css";

const MARKS: Record<string, IconName> = { pending: "clock", failed: "warning", cancelled: "close", passed: "check" };

// A pull request's hosted checks beside it: where they stand in a word or
// two, in their tone, with what failed in the tip, opening the failed check's
// page or the pull request's checks; a reviewer's approval follows as a mark
// of its own.
export function HostedChecksLink({ checks, pr, className }: { checks: HostedChecks; pr: string; className: string }) {
  const look = hostedChecksLook(checks, pr);
  const content = <><Icon name={MARKS[look.tone]} /><span>{look.text}</span></>;
  return <>
    {look.url
      ? <a className={className + " hosted-checks hosted-" + look.tone} href={look.url} target="_blank" rel="noreferrer" aria-label={look.text + ": " + look.tip} data-tip={look.tip}>{content}</a>
      : <span className={className + " hosted-checks hosted-" + look.tone} aria-label={look.text + ": " + look.tip} data-tip={look.tip}>{content}</span>}
    {checks.approved && <span className={className + " hosted-checks hosted-approved"} data-tip="A reviewer approved it"><Icon name="check-double" /><span>Approved</span></span>}
  </>;
}
