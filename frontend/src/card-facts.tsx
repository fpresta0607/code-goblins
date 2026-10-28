import type { Task } from "./types";
import { CardClock } from "./card-clock";
import { CardPullRequest } from "./card-pull-request";
import { HarnessMark } from "./harness-mark";
import { harnessTip } from "./workflow";

// What a goblin's board card says beside its name, project and status, on its
// card on the canvas and in the list the canvas becomes at phone width: its
// clock, its pull request and the mark of the harness it runs.
export function CardFacts({ task, now }: { task: Task; now: number }) {
  return <>
    <CardClock task={task} now={now} />
    <CardPullRequest task={task} />
    {task.harness && <span className="card-harness"><HarnessMark harness={task.harness} label={harnessTip(task.harness, task.model, task.effort)} /></span>}
  </>;
}
