import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { heldSays, heldWho } from "./afk";
import { age } from "./presentation";
import { personaFor } from "./workflow";
import type { AfkHeld, Task } from "./types";
import "./afk.css";

// What is held for the Overlord while AFK mode is on, drawn as the Command
// Center's inbox draws what waits on him: whose it is, what it asks, what
// became of it and what its goblin did meanwhile. Where onOpen is given, an
// item that still waits opens in the Command Center, where he answers it.
export function AfkHeldList({ held, tasks, onOpen }: { held: AfkHeld[]; tasks: Task[]; onOpen?: (item: string) => void }) {
  if (!held.length) return <p className="muted">Nothing is held for you.</p>;
  return <ul className="inbox-list afk-held">{held.map((one) => <li key={one.item + one.at}>
    <Avatar persona={one.task ? personaFor(tasks.find((task) => task.id === one.task)) : "cfo"} small />
    <span className="inbox-text"><strong>{heldWho(one)}</strong><span className="inbox-summary">{one.what}</span><small className={one.waiting ? "afk-waiting" : undefined}>{heldSays(one)}</small></span>
    <time dateTime={one.at}>{age(one.at)}</time>
    {onOpen && one.waiting
      ? <button className="icon-button raised" aria-label={"Answer " + heldWho(one) + ": " + one.what} data-tip="Answer" data-tip-align="end" onClick={() => onOpen(one.item)}><Icon name="command-center" /></button>
      : <span className={"delivery " + (one.waiting ? "uncertain" : "succeeded")}><Icon name={one.waiting ? "clock" : "check"} /></span>}
  </li>)}</ul>;
}
