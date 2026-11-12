import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import { age } from "./presentation";
import { RestartCeoButton } from "./restart-ceo-button";
import type { Run, Snapshot } from "./types";

// The CEO cannot be told why its wakes wait untyped, since that would be one
// more wake, so this item tells the Overlord in plain words and offers one
// thing: the board's own Restart the CEO, the control of the CEO's Harness
// section, which the supervisor carries out itself. It runs no command.
// Once the CEO takes its wakes, the supervisor takes the item back.
export function CeoUnwokenCard({ run, snapshot, now, pager }: { run: Run; snapshot: Snapshot; now: number; pager?: ReactNode }) {
  return <article className="update-card" aria-labelledby={"ceo-unwoken-" + run.id}>
    <header className="update-head">
      <Avatar persona="ceo" small />
      <span className="who"><strong>Code Goblins</strong><small>{age(run.created_at, now)}</small></span>
    </header>
    <h3 id={"ceo-unwoken-" + run.id}>{run.title}</h3>
    <div className="card-actions">
      {pager}
      {run.state === "ready" && <RestartCeoButton snapshot={snapshot} />}
    </div>
  </article>;
}
