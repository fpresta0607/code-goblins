import type { ReactNode } from "react";

// Text he sent or a command printed, which a phone shows by its first lines
// when it is long (isLongText), with Show more under them that opens it whole
// and Show less that folds it again; a short one shows as it is. Whether it
// is open is the view's to keep (open, onOpenChange), and the tap never
// reaches the message, so it never opens the message's own row.
export function LongText({ isLong, open, onOpenChange, children }: { isLong: boolean; open: boolean; onOpenChange: (isOpen: boolean) => void; children: ReactNode }) {
  if (!isLong) return children;
  return <>
    <div className={"long-text" + (open ? "" : " folded")}>{children}</div>
    <button type="button" className="text-button long-text-more" aria-expanded={open}
      onClick={(event) => { event.stopPropagation(); onOpenChange(!open); }}>{open ? "Show less" : "Show more"}</button>
  </>;
}
