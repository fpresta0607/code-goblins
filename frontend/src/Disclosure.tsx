import { useState, type ReactNode } from "react";
import { Icon } from "./Icon";

// The board's one disclosure: a thin chevron that turns down when open, with
// the same spacing and motion everywhere. Load-on-open behavior is adapted from
// SIQshift's shared ShiftGroups: content mounts only while open, so closing
// releases preview resources and a closed section fetches nothing. open and
// onOpenChange let a caller open it, as a failure's log link opens Activity.
export function Disclosure({ title, children, defaultOpen = false, kind = "", id, open: shown, onOpenChange }: {
  title: ReactNode; children: ReactNode; defaultOpen?: boolean; kind?: string; id?: string; open?: boolean; onOpenChange?: (open: boolean) => void;
}) {
  const [own, setOwn] = useState(defaultOpen);
  const open = shown ?? own;
  return <details id={id} className={"disclosure " + kind} open={open} onToggle={(event) => { setOwn(event.currentTarget.open); onOpenChange?.(event.currentTarget.open); }}>
    <summary><Icon name="chevron" />{title}</summary>
    {open && <div className="disclosure-content">{children}</div>}
  </details>;
}
