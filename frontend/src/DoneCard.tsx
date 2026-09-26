import { Icon } from "./Icon";

// The moment after Send: the check draws while the answer is on its way and
// turns green once its asker has it.
export function DoneCard({ heading, label, delivered }: { heading: string; label: string; delivered: boolean }) {
  return <div className={"done-card" + (delivered ? " delivered" : "")} role="status">
    <svg className="check-anim" viewBox="0 0 100 100" aria-hidden="true"><circle cx="50" cy="50" r="46" /><path d="M30 52 44 66 71 37" /></svg>
    <h3>{heading}</h3>
    {label && <p className="received">{delivered && <Icon name="check" />}{label}</p>}
  </div>;
}
