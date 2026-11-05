import { AfkRefusal } from "./afk-refusal";
import type { AfkToggleState } from "./useAfkToggle";
import type { Afk } from "./types";
import "./afk.css";

// The AFK toggle on the CFO's bar and its card, right beside the button that
// opens the CFO: a small switch with its label, whose track spins in place of
// its thumb while the supervisor answers, so the switch keeps its width in the
// row. Only the Overlord turns it, which the supervisor proves of the program
// that asks, and a refusal is one short sentence under the switch, over the
// board, until he closes it. Turning it on asks first, in the question the
// board draws (useAfkToggle). Turning it off needs no question: it only gives
// him his decisions back, and it is how a switch that cannot be read is reset,
// which muted words beside it say while it cannot be read.
// It turns on and off on his private board as on the PC: his decision of
// 2026-10-29, knowing the private board is proven his only by headers a
// program on the PC can forge.
// His last report reopens from the CFO's Task view, not from here (the
// Overlord, 2026-10-29: the last AFK report "could be something that you know
// is part of like the task panel").
export function AfkToggle({ afk, toggle }: { afk: Afk; toggle: AfkToggleState }) {
  const { pending, problem, pressedOn, isAsking, press, clear } = toggle;
  return <div className="afk-header">
    {afk.state === "unreadable" && <span className="muted">Unreadable: a press resets it</span>}
    <button className="afk-toggle" role="switch" aria-checked={afk.state === "on"} aria-label="AFK mode" aria-busy={pending} disabled={pending} onClick={() => press(afk.state)}>
      <span className="afk-toggle-label">AFK</span>
      <span className="afk-toggle-track" aria-hidden="true">{pending ? <span className="card-start-spinner" /> : <span className="afk-toggle-thumb" />}</span>
    </button>
    {!isAsking && <AfkRefusal on={pressedOn} problem={problem} onClose={clear} />}
  </div>;
}
