import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import { age } from "./presentation";
import { ReopenCfoButton } from "./reopen-cfo-button";
import type { Run, Snapshot } from "./types";

// The supervisor brings back a CFO whose terminal ended while nobody asked it
// to stop, and stops after it has ended three times in a row soon after it
// started. This item tells the Overlord so in plain words and offers one
// thing: the board's own Reopen the CFO. It runs no command. Once a CFO runs
// again, the supervisor takes the item back.
export function CfoStoppedCard({ run, snapshot, pager }: { run: Run; snapshot: Snapshot; pager?: ReactNode }) {
  return <article className="update-card" aria-labelledby={"cfo-stopped-" + run.id}>
    <header className="update-head">
      <Avatar persona="cfo" small />
      <span className="who"><strong>Code Goblins</strong><small>{age(run.created_at)}</small></span>
    </header>
    <h3 id={"cfo-stopped-" + run.id}>{run.title}</h3>
    <div className="card-actions">
      {pager}
      {run.state === "ready" && <ReopenCfoButton snapshot={snapshot} />}
    </div>
  </article>;
}
