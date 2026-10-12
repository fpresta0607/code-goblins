import type { Task } from "./types";
import { Icon } from "./Icon";
import { pullRequestIcon, pullRequestLabel, safePullRequest, taskColumn } from "./workflow";

// A goblin's pull request, the same on its board card and its canvas card: its
// number wearing GitHub's icon for what it really did, which opens it. A
// finished card's names its branch in its tip.
export function CardPullRequest({ task }: { task: Task }) {
  const pr = safePullRequest(task.pr), icon = pullRequestIcon(task);
  if (!pr) return null;
  return <a className={"card-pr pr-" + icon} href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)} {...taskColumn(task) === "Completed" && task.branch ? { "data-tip": task.branch } : {}}><Icon name={icon} />{pullRequestLabel(pr)}</a>;
}
