import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import { age } from "./presentation";
import { ReopenCeoButton } from "./reopen-ceo-button";
import type { Run, Snapshot } from "./types";

// The supervisor brings back a CEO whose terminal ended while nobody asked it
// to stop, and stops after it has ended three times in a row soon after it
// started, or when it could not bring it back. This item tells the Overlord
// so in plain words and offers one thing: the board's own Reopen the CEO. It
// runs no command. Once a CEO runs again, the supervisor takes the item back.
export function CeoStoppedCard({ run, snapshot, now, pager }: { run: Run; snapshot: Snapshot; now: number; pager?: ReactNode }) {
  return <article className="update-card" aria-labelledby={"ceo-stopped-" + run.id}>
    <header className="update-head">
      <Avatar persona="ceo" small />
      <span className="who"><strong>Code Goblins</strong><small>{age(run.created_at, now)}</small></span>
    </header>
    <h3 id={"ceo-stopped-" + run.id}>{run.title}</h3>
    <div className="card-actions">
      {pager}
      {run.state === "ready" && <ReopenCeoButton snapshot={snapshot} />}
    </div>
  </article>;
}
