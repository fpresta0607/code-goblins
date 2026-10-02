import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import type { PanelView } from "./GoblinPanel";
import { Icon, type IconName } from "./Icon";
import { rowFit } from "./panel-fit";
import { PanelMore } from "./panel-more";

// A control of the panel's top row: an icon button while the row has room for
// it, and a named item of the row's More menu once it has not. name is what
// its tip and its menu item say, label its accessible name as an icon, and
// importance its claim on the row: where there is not room for all, the
// controls with the highest stay. A control that is a switch says in pressed
// whether it is on.
export interface PanelControl { id: string; name: string; label: string; icon: IconName; importance: number; onPress: () => void; pressed?: boolean }

// The importance of every control of the row, written once. The Task and
// Terminal switch and the corner button, Close or Back, are not controls:
// they always show. A control that joins the row takes its place here.
export const PANEL_IMPORTANCE = { maximize: 20, window: 10 } as const;

const VIEWS: { id: PanelView; name: string; icon: IconName }[] = [{ id: "task", name: "Task", icon: "task" }, { id: "terminal", name: "Terminal", icon: "terminal" }];
// A control's width, an icon button's in styles.css, and the row's gap there.
const CONTROL = 44;
const GAP = 8;

// The top row of every panel, on one line at any width the panel can take:
// the Task and Terminal switch, when the panel has both views, then the
// controls, then corner, the Close or Back button. Nothing in it overlaps or
// is cut: the controls the row cannot hold at full size go into More, the
// least important first, and at the narrowest widths the switch shows its
// icons without its words (see rowFit). notice is drawn under the row's end,
// such as why Open in terminal was refused.
export function PanelRow({ view, onView, controls, corner, notice }: {
  view?: PanelView; onView?: (view: PanelView) => void; controls: PanelControl[]; corner: ReactNode; notice?: ReactNode;
}) {
  const row = useRef<HTMLDivElement>(null);
  const cornerBox = useRef<HTMLDivElement>(null);
  const words = useRef<HTMLDivElement>(null);
  const icons = useRef<HTMLDivElement>(null);
  const [fit, setFit] = useState({ shown: controls.length, hasWords: true });
  const count = controls.length;
  useLayoutEffect(() => {
    const measure = () => {
      if (!row.current || !cornerBox.current) return;
      const style = getComputedStyle(row.current);
      const width = row.current.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
      const next = rowFit({ width, switchWords: words.current?.offsetWidth ?? 0, switchIcons: icons.current?.offsetWidth ?? 0, corner: cornerBox.current.offsetWidth, control: CONTROL, gap: GAP, count });
      setFit((prior) => prior.shown === next.shown && prior.hasWords === next.hasWords ? prior : next);
    };
    measure();
    const observer = new ResizeObserver(measure);
    for (const box of [row.current, cornerBox.current, words.current]) if (box) observer.observe(box);
    return () => observer.disconnect();
  }, [count]);
  const kept = new Set([...controls].sort((a, b) => b.importance - a.importance).slice(0, fit.shown).map((control) => control.id));
  const pill = (hasWords: boolean, measured?: typeof words) => <div ref={measured} className={"panel-pill" + (hasWords ? "" : " icons")} role={measured ? undefined : "group"} aria-label={measured ? undefined : "Panel view"}>
    {VIEWS.map(({ id, name, icon }) => <button key={id} aria-pressed={view === id} {...(hasWords ? {} : { "aria-label": name, "data-tip": name })} onClick={() => onView?.(id)}><Icon name={icon} />{hasWords && name}</button>)}
  </div>;
  return <div ref={row} className="panel-top">
    <span />
    {view ? pill(fit.hasWords) : <span />}
    <div className="panel-controls">
      {controls.filter((control) => kept.has(control.id)).map((control) => <button key={control.id} className="icon-button" aria-label={control.label} aria-pressed={control.pressed} data-tip={control.name} data-tip-align="end" onClick={control.onPress}><Icon name={control.icon} /></button>)}
      {kept.size < count && <PanelMore controls={controls.filter((control) => !kept.has(control.id)).sort((a, b) => b.importance - a.importance)} />}
      <div ref={cornerBox} className="panel-corner">{corner}</div>
      {notice}
    </div>
    {/* The switch in both its forms, out of sight and taking no room, so the
        row knows how wide each is whichever one it shows. */}
    {view && <div className="panel-measure" aria-hidden="true" inert>{pill(true, words)}{pill(false, icons)}</div>}
  </div>;
}
