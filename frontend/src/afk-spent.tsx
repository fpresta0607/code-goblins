import { Icon, type IconName } from "./Icon";
import { allowanceSays, type AfkAllowance } from "./afk";
import "./afk.css";

// The mark each provider wears under Spent.
const MARKS: Record<string, IconName> = { claude: "claude", codex: "codex", pi: "pi" };

// ARROW points down at the percent it marks.
const ARROW = "M1 1h10L6 8Z";

// What was spent while AFK mode was on, one allowance to a row: its mark, its
// name and a small graph of the percent used, with a red arrow where it stood
// when AFK turned on and where it stood when it turned off. Credits have no
// percent, so a credit balance says what was spent.
export function AfkSpent({ spent }: { spent: AfkAllowance[] }) {
  return <ul className="afk-spent">{spent.map((allowance) => {
    const says = allowanceSays(allowance);
    const { on, off } = allowance;
    const last = off ?? on ?? 0;
    return <li key={allowance.provider + " " + allowance.window}>
      <span className="afk-spent-mark"><Icon name={MARKS[allowance.provider] ?? "sparkle"} /></span>
      <span className="afk-spent-name">{says.name}</span>
      {!allowance.credits && <span className="afk-spent-graph" role="img" aria-label={says.label}>
        <span className="afk-spent-fill" style={{ width: last + "%" }} />
        {on !== null && off !== null && !allowance.reset && off > on && <span className="afk-spent-gain" style={{ left: on + "%", width: off - on + "%" }} />}
        {on !== null && off !== null && <svg className="afk-spent-arrow then" style={{ left: on + "%" }} viewBox="0 0 12 9" aria-hidden="true" focusable="false"><path d={ARROW} /></svg>}
        <svg className="afk-spent-arrow" style={{ left: last + "%" }} viewBox="0 0 12 9" aria-hidden="true" focusable="false"><path d={ARROW} /></svg>
      </span>}
      <span className="afk-spent-value">{allowance.reset && <Icon name="refresh" />}{says.value}</span>
    </li>;
  })}</ul>;
}
