import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import { age } from "./presentation";
import { RestartCfoButton } from "./restart-cfo-button";
import type { Run, Snapshot } from "./types";

// The CFO cannot be told why its wakes wait untyped, since that would be one
// more wake, so this item tells the Overlord in plain words and offers one
// thing: the board's own Restart the CFO, the button and confirmation of the
// CFO's panel, which the supervisor carries out itself. It runs no command.
// Once the CFO takes its wakes, the supervisor takes the item back.
export function CfoUnwokenCard({ run, snapshot, pager }: { run: Run; snapshot: Snapshot; pager?: ReactNode }) {
  return <article className="update-card" aria-labelledby={"cfo-unwoken-" + run.id}>
    <header className="update-head">
      <Avatar persona="cfo" small />
      <span className="who"><strong>Code Goblins</strong><small>{age(run.created_at)}</small></span>
    </header>
    <h3 id={"cfo-unwoken-" + run.id}>{run.title}</h3>
    <div className="card-actions">
      {pager}
      {run.state === "ready" && <RestartCfoButton snapshot={snapshot} isCompact={false} />}
    </div>
  </article>;
}
