import type { Task } from "./types";

export function CardRouting({ task }: { task: Task }) {
  return <>
    {task.harness && <span className="card-engine" data-tip={task.model}>{task.model && task.model !== "default" ? task.model : "Model not recorded"}{task.effort && task.effort !== "default" && " · " + task.effort}</span>}
    {task.route_lane && <span className="card-route" data-tip={task.route_reason || task.route_lane}>{task.route_reason || task.route_lane}</span>}
  </>;
}
