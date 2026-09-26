import { useRef, type RefObject } from "react";
import { paneWidth } from "./terminalOrder";

// The handle between the board and the panel: drag it, or focus it and use
// the arrow keys, to give the terminal the columns it needs. The width is kept
// for this browser once the drag ends.
export function PaneDivider({ workspace, pane, width, onWidth, onDone }: { workspace: RefObject<HTMLDivElement | null>; pane: RefObject<HTMLElement | null>; width: number | null; onWidth: (width: number) => void; onDone: (width: number) => void }) {
  const dragging = useRef(0);
  const set = (next: number) => {
    const bounds = workspace.current?.getBoundingClientRect();
    if (!bounds) return 0;
    const clamped = paneWidth(next, bounds.width);
    onWidth(clamped);
    return clamped;
  };
  return <div className="pane-divider" role="separator" aria-orientation="vertical" aria-label="Resize the panel" aria-valuenow={width ?? undefined} tabIndex={0}
    onPointerDown={(event) => { dragging.current = width ?? pane.current?.getBoundingClientRect().width ?? 0; event.currentTarget.setPointerCapture(event.pointerId); event.preventDefault(); }}
    onPointerMove={(event) => {
      const bounds = workspace.current?.getBoundingClientRect();
      if (dragging.current && bounds) dragging.current = set(bounds.right - event.clientX);
    }}
    onPointerUp={(event) => {
      if (!dragging.current) return;
      event.currentTarget.releasePointerCapture(event.pointerId);
      onDone(dragging.current);
      dragging.current = 0;
    }}
    onKeyDown={(event) => {
      if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
      event.preventDefault();
      const current = width ?? pane.current?.getBoundingClientRect().width ?? 0;
      const next = set(current + (event.key === "ArrowLeft" ? 48 : -48));
      if (next) onDone(next);
    }} />;
}
