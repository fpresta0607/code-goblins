import { Icon, type IconName } from "./Icon";
import type { PullRequestTest } from "./pull-request-test";
import "./hosted-checks.css";

const MARKS: Record<string, IconName> = { pending: "clock", failed: "warning", cancelled: "close", passed: "check" };

// Where a goblin's pull request stands under test, beside it: on a merge
// train in the train's words, opening the train's pull request, else its
// hosted checks in a word or two, opening their run or the failed check's
// page, in their tone, with the detail in the tip; a reviewer's approval
// follows as a mark of its own. isIconOnly draws the marks alone and puts
// their words in the tips too.
export function PullRequestTestLink({ test, approved, className, isIconOnly = false }: { test: PullRequestTest; approved: boolean; className: string; isIconOnly?: boolean }) {
  const content = <><Icon name={MARKS[test.tone]} />{!isIconOnly && <span>{test.text}</span>}</>;
  const tip = isIconOnly ? test.text + ": " + test.tip : test.tip;
  return <>
    {test.url
      ? <a className={className + " hosted-checks hosted-" + test.tone} href={test.url} target="_blank" rel="noreferrer" aria-label={test.text + ": " + test.tip} data-tip={tip}>{content}</a>
      : <span className={className + " hosted-checks hosted-" + test.tone} aria-label={test.text + ": " + test.tip} data-tip={tip}>{content}</span>}
    {approved && <span className={className + " hosted-checks hosted-approved"} data-tip={isIconOnly ? "Approved: a reviewer approved it" : "A reviewer approved it"}><Icon name="check-double" />{!isIconOnly && <span>Approved</span>}</span>}
  </>;
}
