import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { Icon, type IconName } from "./Icon";
import { rowFit } from "./panel-fit";
import { PanelMore } from "./panel-more";

// A control of the panel's top row: an icon button while the row has room for
// it, and a named item of the row's More menu once it has not. name is what
// its menu item says, label its accessible name as an icon, and
// importance its claim on the row: where there is not room for all, the
// controls with the highest stay.
export interface PanelControl { id: string; name: string; label: string; icon: IconName; importance: number; onPress: () => void }

// The importance of every control of the row, written once. The corner
// button, Close or Back, always shows. A control that joins the row takes
// its place here.
export const PANEL_IMPORTANCE = { style: 30, maximize: 20, window: 10 } as const;

// A control's width, an icon button's in styles.css, and the row's gap there.
const CONTROL = 44;
const GAP = 8;
// The least a header's one row keeps for the session's mark and name, about
// the name's first three letters, beyond the floor the part that gives way
// never narrows past, its picture, before a control that can go into More
// takes the room.
const NAME_ROOM = 80;

// sharedRoom is what a header's one row leaves its controls: its width less
// its padding, the gaps between its parts, each part that keeps its width,
// such as the Chat and Task switch, and for the part that gives way, the
// session's picture, name and mark, its floor (its min-width) and NAME_ROOM.
function sharedRoom(controls: HTMLElement): number {
  const header = controls.parentElement!;
  const style = getComputedStyle(header);
  const parts = [...header.children].filter((part): part is HTMLElement => part !== controls && part instanceof HTMLElement);
  const kept = parts.reduce((sum, part) => {
    const partStyle = getComputedStyle(part);
    return sum + parseFloat(style.columnGap) + (partStyle.flexShrink === "0" ? part.offsetWidth : parseFloat(partStyle.minWidth) + NAME_ROOM);
  }, 0);
  return header.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight) - kept;
}

// The panel's controls, on one line at any width the panel can take: what
// leads them, such as the CFO's AFK switch, then its controls, then corner,
// the Close or Back button. Controls that cannot fit go into More, least
// important first. notice is drawn under the row's end, such as what a press
// of Open in terminal met. In a header's one row, isShared, the controls are
// as wide as what they show, never narrower than what always shows: what
// leads them, More and the corner button, and show what fits in all the room
// the row's other parts leave (sharedRoom).
export function PanelRow({ controls, corner, notice, leading, isShared = false }: {
  controls: PanelControl[]; corner: ReactNode; notice?: ReactNode; leading?: ReactNode; isShared?: boolean;
}) {
  const row = useRef<HTMLDivElement>(null);
  const cornerBox = useRef<HTMLDivElement>(null);
  const leadingBox = useRef<HTMLDivElement>(null);
  const [fit, setFit] = useState({ shown: controls.length });
  const count = controls.length;
  const isLed = !!leading;
  useLayoutEffect(() => {
    const measure = () => {
      if (!row.current || !cornerBox.current) return;
      const style = getComputedStyle(row.current);
      const padding = parseFloat(style.paddingLeft) + parseFloat(style.paddingRight);
      const lead = leadingBox.current ? leadingBox.current.offsetWidth + GAP : 0;
      const corner = cornerBox.current.offsetWidth;
      const next = rowFit({ width: (isShared ? sharedRoom(row.current) : row.current.clientWidth) - padding - lead, corner, control: CONTROL, gap: GAP, count });
      if (isShared) row.current.style.minWidth = padding + lead + corner + (next.shown + (next.shown < count ? 1 : 0)) * (CONTROL + GAP) + "px";
      setFit((prior) => prior.shown === next.shown ? prior : next);
    };
    measure();
    // A new width is fitted before the browser draws it, so no frame shows
    // the row as it fitted the old one.
    const observer = new ResizeObserver(() => flushSync(measure));
    for (const box of [row.current, cornerBox.current, leadingBox.current, isShared ? row.current?.parentElement : null]) if (box) observer.observe(box);
    return () => observer.disconnect();
  }, [count, isLed, isShared]);
  const kept = new Set([...controls].sort((a, b) => b.importance - a.importance).slice(0, fit.shown).map((control) => control.id));
  return <div ref={row} className="panel-top">
    <div className="panel-controls">
      {leading && <div ref={leadingBox} className="panel-leading">{leading}</div>}
      {controls.filter((control) => kept.has(control.id)).map((control) => <button key={control.id} className="icon-button" aria-label={control.label} onClick={control.onPress}><Icon name={control.icon} /></button>)}
      {kept.size < count && <PanelMore controls={controls.filter((control) => !kept.has(control.id)).sort((a, b) => b.importance - a.importance)} />}
      <div ref={cornerBox} className="panel-corner">{corner}</div>
      {notice}
    </div>
  </div>;
}
