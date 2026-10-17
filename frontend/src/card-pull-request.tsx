import type { Task } from "./types";
import { Icon } from "./Icon";
import { pullRequestIcon, pullRequestLabel, safePullRequest, taskColumn } from "./workflow";

// A goblin's pull request on its board card and its canvas card: its number
// wearing GitHub's icon for what it really did, which opens it. A finished
// card's names its branch in its tip. isIconOnly, as on the board's row,
// draws the icon alone and puts the number in the tip too.
export function CardPullRequest({ task, isIconOnly = false }: { task: Task; isIconOnly?: boolean }) {
  const pr = safePullRequest(task.pr), icon = pullRequestIcon(task);
  if (!pr) return null;
  const branch = taskColumn(task) === "Completed" ? task.branch : "";
  const tip = isIconOnly ? [pullRequestLabel(pr), branch].filter(Boolean).join(" · ") : branch;
  return <a className={"card-pr pr-" + icon} href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)} {...tip ? { "data-tip": tip } : {}}><Icon name={icon} />{!isIconOnly && pullRequestLabel(pr)}</a>;
}
