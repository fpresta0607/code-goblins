import { Icon, type IconName } from "./Icon";
import { allowanceGraph, allowanceSays, type AfkAllowance } from "./afk";
import "./afk.css";

// The mark each provider wears under Spent.
const MARKS: Record<string, IconName> = { claude: "claude", codex: "codex", pi: "pi" };

// What was spent while AFK mode was on, one weekly limit or credit balance to
// a row: its mark, its name, a bar of the limit and how much of it is left.
// Gray is what was used before AFK turned on, green, with a green arrow from
// where it stood then to where it stood when AFK turned off, is what AFK used,
// which the chip beside what is left says too, and the empty rest is what is
// left. Credits have no percent, so a credit balance says what was spent.
export function AfkSpent({ spent }: { spent: AfkAllowance[] }) {
  return <ul className="afk-spent">{spent.map((allowance) => {
    const says = allowanceSays(allowance);
    const graph = allowanceGraph(allowance);
    const used = graph !== null && graph.to > graph.from;
    return <li key={allowance.provider + " " + allowance.window}>
      <span className="afk-spent-mark"><Icon name={MARKS[allowance.provider] ?? "sparkle"} /></span>
      <span className="afk-spent-name">{says.name}</span>
      {graph && <span className="afk-spent-graph" role="img" aria-label={says.label}>
        <span className="afk-spent-before" style={{ width: graph.before + "%" }} />
        {used && <span className="afk-spent-used" style={{ left: graph.from + "%", width: graph.to - graph.from + "%" }} />}
        {used && <span className="afk-spent-arrow" style={{ left: graph.from + "%", width: graph.to - graph.from + "%" }} />}
      </span>}
      <span className="afk-spent-value">
        {says.value}
        {says.change && <small className={"afk-spent-change" + (used && !allowance.reset ? " used" : "")}>{allowance.reset && <Icon name="refresh" />}{says.change}</small>}
      </span>
    </li>;
  })}</ul>;
}
