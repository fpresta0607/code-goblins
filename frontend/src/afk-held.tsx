import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { heldRecommends, heldSays, heldWho } from "./afk";
import { age } from "./presentation";
import { personaFor } from "./workflow";
import type { AfkHeld, Task } from "./types";
import "./afk.css";
import "./lantern.css";

// What still waits on the Overlord from a stretch of AFK mode, drawn as the
// Command Center's inbox draws what waits on him: whose it is, what it asks,
// what was recommended for it, where it stands and what its goblin did
// meanwhile.
export function AfkHeldList({ held, tasks }: { held: AfkHeld[]; tasks: Task[] }) {
  return <ul className="inbox-list afk-held">{held.map((one) => <li key={one.item + one.at}>
    <Avatar persona={one.task ? personaFor(tasks.find((task) => task.id === one.task)) : "cfo"} small />
    <span className="inbox-text"><strong>{heldWho(one)}</strong><span className="inbox-summary">{one.what}</span>{one.recommendation && <small className="afk-recommends">{heldRecommends(one)}</small>}<small className="afk-waiting">{heldSays(one)}</small></span>
    <time dateTime={one.at}>{age(one.at)}</time>
    <span className="delivery uncertain"><Icon name="clock" /></span>
  </li>)}</ul>;
}
