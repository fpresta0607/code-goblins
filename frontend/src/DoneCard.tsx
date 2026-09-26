import type { ReactNode } from "react";
import { Icon } from "./Icon";

// The moment after Send: the check draws at once while the answer goes on its
// way quietly, and says it was delivered if that lands while it shows.
export function DoneCard({ heading, label, pager }: { heading: string; label: string; pager?: ReactNode }) {
  return <div className="done-card" role="status">
    <svg className="check-anim" viewBox="0 0 100 100" aria-hidden="true"><circle cx="50" cy="50" r="46" /><path d="M30 52 44 66 71 37" /></svg>
    <h3>{heading}</h3>
    {label && <p className="received"><Icon name="check" />{label}</p>}
    {pager && <div className="card-actions">{pager}</div>}
  </div>;
}
