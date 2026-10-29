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

// The top row of every panel, on one line at any width the panel can take:
// its controls, then corner, the Close or Back button. Controls that cannot
// fit go into More, least important first. notice is drawn under the row's
// end, such as what a press of Open in terminal met.
export function PanelRow({ controls, corner, notice }: {
  controls: PanelControl[]; corner: ReactNode; notice?: ReactNode;
}) {
  const row = useRef<HTMLDivElement>(null);
  const cornerBox = useRef<HTMLDivElement>(null);
  const [fit, setFit] = useState({ shown: controls.length });
  const count = controls.length;
  useLayoutEffect(() => {
    const measure = () => {
      if (!row.current || !cornerBox.current) return;
      const style = getComputedStyle(row.current);
      const width = row.current.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
      const next = rowFit({ width, corner: cornerBox.current.offsetWidth, control: CONTROL, gap: GAP, count });
      setFit((prior) => prior.shown === next.shown ? prior : next);
    };
    measure();
    // A new width is fitted before the browser draws it, so no frame shows
    // the row as it fitted the old one.
    const observer = new ResizeObserver(() => flushSync(measure));
    for (const box of [row.current, cornerBox.current]) if (box) observer.observe(box);
    return () => observer.disconnect();
  }, [count]);
  const kept = new Set([...controls].sort((a, b) => b.importance - a.importance).slice(0, fit.shown).map((control) => control.id));
  return <div ref={row} className="panel-top">
    <div className="panel-controls">
      {controls.filter((control) => kept.has(control.id)).map((control) => <button key={control.id} className="icon-button" aria-label={control.label} onClick={control.onPress}><Icon name={control.icon} /></button>)}
      {kept.size < count && <PanelMore controls={controls.filter((control) => !kept.has(control.id)).sort((a, b) => b.importance - a.importance)} />}
      <div ref={cornerBox} className="panel-corner">{corner}</div>
      {notice}
    </div>
  </div>;
}
