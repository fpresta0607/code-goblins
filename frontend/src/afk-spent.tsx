import { Icon, type IconName } from "./Icon";
import { allowanceGraph, allowanceSays, diskGraph, diskSays, type AfkAllowance, type AfkDisk } from "./afk";
import "./afk.css";

// The mark each provider wears under Spent.
const MARKS: Record<string, IconName> = { claude: "claude", codex: "codex", pi: "pi" };

// What was spent while AFK mode was on, one weekly limit or credit balance to
// a row, and free disk on the row after them: its mark, its name, a bar of the
// limit or the drive and how much of it is left. Gray is what was used before
// AFK turned on, green, with a green arrow from where it stood then to where
// it stood when AFK turned off, is what AFK used, which the chip beside what
// is left says too, and the empty rest is what is left. Over disk that AFK
// freed the arrow points back. Credits have no percent, so a credit balance
// says what was spent.
export function AfkSpent({ spent, disk }: { spent: AfkAllowance[]; disk: AfkDisk | null }) {
  const rows = spent.map((allowance) => ({ key: allowance.provider + " " + allowance.window, mark: MARKS[allowance.provider] ?? "sparkle", says: allowanceSays(allowance), graph: allowanceGraph(allowance), reset: allowance.reset }));
  if (disk) rows.push({ key: "disk", mark: "drive", says: diskSays(disk), graph: diskGraph(disk), reset: false });
  return <ul className="afk-spent">{rows.map(({ key, mark, says, graph, reset }) => {
    const moved = graph !== null && graph.to !== graph.from;
    const freed = graph !== null && graph.to < graph.from;
    const stretch = graph ? { left: Math.min(graph.from, graph.to) + "%", width: Math.round(Math.abs(graph.to - graph.from) * 100) / 100 + "%" } : undefined;
    return <li key={key}>
      <span className="afk-spent-mark"><Icon name={mark} /></span>
      <span className="afk-spent-name">{says.name}</span>
      {graph && <span className="afk-spent-graph" role="img" aria-label={says.label}>
        <span className="afk-spent-before" style={{ width: graph.before + "%" }} />
        {moved && <span className="afk-spent-used" style={stretch} />}
        {moved && <span className={"afk-spent-arrow" + (freed ? " back" : "")} style={stretch} />}
      </span>}
      <span className="afk-spent-value">
        {says.value}
        {says.change && <small className={"afk-spent-change" + (moved && !reset ? " used" : "")}>{reset && <Icon name="refresh" />}{says.change}</small>}
      </span>
    </li>;
  })}</ul>;
}
