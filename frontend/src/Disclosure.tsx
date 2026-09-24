import { useState, type ReactNode } from "react";
import { Icon } from "./Icon";

// The board's one disclosure: a thin chevron that turns down when open, with
// the same spacing and motion everywhere. Load-on-open behavior is adapted from
// SIQshift's shared ShiftGroups: content mounts only while open, so closing
// releases preview resources.
export function Disclosure({ title, children, defaultOpen = false, kind = "" }: { title: ReactNode; children: ReactNode; defaultOpen?: boolean; kind?: string }) {
  const [open, setOpen] = useState(defaultOpen);
  return <details className={"disclosure " + kind} open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary><Icon name="chevron" />{title}</summary>
    {open && <div className="disclosure-content">{children}</div>}
  </details>;
}
