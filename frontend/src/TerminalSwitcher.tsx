import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { asksOverlord, nodeStatus, personaFor } from "./workflow";
import type { DeckEntry } from "./terminalOrder";

// Every terminal one click away, the CFO first; the first nine are also
// Ctrl+Alt+1 to 9, and Ctrl+Alt+Up and Down step through them.
export function TerminalSwitcher({ order, current, snapshot, onSwitch }: { order: DeckEntry[]; current: string; snapshot: Snapshot; onSwitch: (key: string) => void }) {
  return <nav className="terminal-switcher" aria-label="Terminals">
    <ol>
      {order.map((entry, index) => {
        const task = entry.task;
        const title = task ? task.title || task.id : "CFO";
        const status = task ? nodeStatus({ id: task.id, title, task, relation: "" }, asksOverlord(snapshot, task.id)) : snapshot.registration ? "Registration stale" : "Supervising";
        const shortcut = index < 9 ? "Ctrl+Alt+" + (index + 1) : "";
        return <li key={entry.key}>
          <button className={"phase-" + (task ? task.phase : snapshot.registration ? "stale" : "working")} aria-current={entry.key === current ? "true" : undefined}
            aria-label={title + ", " + status + (shortcut ? ", " + shortcut : "")} onClick={() => onSwitch(entry.key)}>
            <Avatar persona={task ? personaFor(task) : "cfo"} small />
            <span className="switcher-name">{title}</span>
            <span className="status-dot" aria-hidden="true" />
            {shortcut && <kbd aria-hidden="true">{index + 1}</kbd>}
          </button>
        </li>;
      })}
    </ol>
  </nav>;
}
