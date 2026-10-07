import type { FleetTree, TreeNode } from "./types";
import { age } from "./presentation";
import { BabyGoblin } from "./BabyGoblin";
import { Chevron } from "./Chevron";
import { babyFor, finished, forHowLong, formatMemory, isDimmed, phaseOf, running, stateWord, summarize } from "./fleet-tree";

// One row of What's working: the child's baby goblin, what it is doing (its
// own words, or a silent one's last line), its state, for how long, and its
// memory.
function WorkingRow({ node, now }: { node: TreeNode; now: number }) {
  const phase = phaseOf(node);
  const said = node.state === "silent" || node.state === "failed" ? node.last_line : "";
  return <li className={"working-row" + (isDimmed(node) ? " dim" : "")} data-state={node.state}>
    <BabyGoblin baby={babyFor(node)} silent={node.state === "silent"} />
    <span className="working-what"><strong>{node.label}</strong>
      {said ? <code className="working-last-line">{said}</code> : node.detail && <small>{node.detail}</small>}</span>
    <span className="working-meta">
      <span className={"plain-status phase-" + phase}><span className="status-dot" />{stateWord(node)}</span>
      <span className="working-for">{forHowLong(node, now)}</span>
      <span className="working-memory">{formatMemory(node.memory)}</span>
    </span>
  </li>;
}

// What's working in a goblin's panel: counts and states first, then one row
// for each child still running, the finished ones folded away, and when the
// supervisor read it all.
export function WhatsWorking({ tree, now }: { tree: FleetTree; now: number }) {
  const summary = summarize(tree), active = running(tree), ended = finished(tree);
  return <div className="whats-working">
    <p className="working-summary">
      {summary.working > 0 && <span className="plain-status phase-working"><span className="status-dot" />{summary.working} working</span>}
      {summary.silent > 0 && <span className="plain-status phase-silent"><span className="status-dot" />{summary.silent} silent</span>}
      {summary.idle > 0 && <span className="plain-status phase-idle"><span className="status-dot" />{summary.idle} idle</span>}
      {active.length === 0 && <span className="muted">Nothing running now</span>}
      {tree.memory > 0 && <span>{formatMemory(tree.memory)} in all</span>}
    </p>
    {active.length > 0 && <ul className="working-list" aria-label="Running">{active.map((node) => <WorkingRow key={node.id} node={node} now={now} />)}</ul>}
    {ended.length > 0 && <details className="working-finished">
      <summary><Chevron collapsed />{ended.length} finished</summary>
      <ul className="working-list" aria-label="Finished">{ended.map((node) => <WorkingRow key={node.id} node={node} now={now} />)}</ul>
    </details>}
    <p className="working-freshness">Read {age(tree.fetched_at)} from the goblin's records and processes.{tree.own_memory > 0 && ` The goblin itself holds ${formatMemory(tree.own_memory)}.`}</p>
    {tree.unread.length > 0 && <p className="working-unread">Not read: {tree.unread.join("; ")}</p>}
  </div>;
}
