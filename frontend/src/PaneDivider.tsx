import { useEffect, useRef, type PointerEvent, type RefObject } from "react";
import { paneWidth } from "./terminalOrder";
import { PANEL_RESIZED } from "./terminalInput";

// The handle between the board and the panel: drag it, or focus it and use
// the arrow keys, to give the terminal the columns it needs. The width is kept
// for this browser once the drag ends. A drag lays the board out at most once
// an animation frame, however many moves arrive, and reports that the panel is
// resizing so a terminal previews the panel instead of refitting on every
// move; the drag's end refits it once.
export function PaneDivider({ workspace, pane, width, onWidth, onResizing, onDone }: { workspace: RefObject<HTMLDivElement | null>; pane: RefObject<HTMLElement | null>; width: number | null; onWidth: (width: number) => void; onResizing: (resizing: boolean) => void; onDone: (width: number) => void }) {
  const dragging = useRef(0);
  const pointer = useRef<{ x: number; frame: number } | null>(null);
  const set = (next: number) => {
    const bounds = workspace.current?.getBoundingClientRect();
    if (!bounds) return 0;
    const clamped = paneWidth(next, bounds.width);
    onWidth(clamped);
    return clamped;
  };
  const follow = (x: number) => {
    const bounds = workspace.current?.getBoundingClientRect();
    if (dragging.current && bounds) dragging.current = set(bounds.right - x);
  };
  const end = (event: PointerEvent<HTMLDivElement>) => {
    if (!dragging.current) return;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    if (pointer.current) {
      cancelAnimationFrame(pointer.current.frame);
      follow(pointer.current.x);
      pointer.current = null;
    }
    onDone(dragging.current);
    dragging.current = 0;
    onResizing(false);
    window.dispatchEvent(new Event(PANEL_RESIZED));
  };
  useEffect(() => () => {
    if (!dragging.current) return;
    if (pointer.current) cancelAnimationFrame(pointer.current.frame);
    onResizing(false);
    window.dispatchEvent(new Event(PANEL_RESIZED));
  }, [onResizing]);
  return <div className="pane-divider" role="separator" aria-orientation="vertical" aria-label="Resize the panel" aria-valuenow={width ?? undefined} tabIndex={0}
    onPointerDown={(event) => {
      dragging.current = width ?? pane.current?.getBoundingClientRect().width ?? 0;
      onResizing(true);
      event.currentTarget.setPointerCapture(event.pointerId);
      event.preventDefault();
    }}
    onPointerMove={(event) => {
      if (!dragging.current) return;
      if (pointer.current) { pointer.current.x = event.clientX; return; }
      pointer.current = { x: event.clientX, frame: requestAnimationFrame(() => { const x = pointer.current?.x; pointer.current = null; if (x !== undefined) follow(x); }) };
    }}
    onPointerUp={end}
    onPointerCancel={end}
    onKeyDown={(event) => {
      if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
      event.preventDefault();
      const current = width ?? pane.current?.getBoundingClientRect().width ?? 0;
      const next = set(current + (event.key === "ArrowLeft" ? 48 : -48));
      if (next) onDone(next);
    }} />;
}
