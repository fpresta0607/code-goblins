import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { Icon } from "./Icon";
import type { PanelControl } from "./panel-row";

// The controls the row has no room for, each with its icon and its name. The
// menu opens on its first item and closes on a choice or on Escape, which hand
// the keyboard back to More, and on a press anywhere else; Up and Down move
// through it. When the row finds room for every control again, More goes and
// the keyboard it held goes to the panel.
export function PanelMore({ controls }: { controls: PanelControl[] }) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    box.current?.querySelector<HTMLElement>("[role^=menuitem]")?.focus();
    const away = (event: PointerEvent) => { if (event.target instanceof Node && !box.current?.contains(event.target)) setOpen(false); };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);
  useLayoutEffect(() => {
    const more = box.current;
    return () => { if (more?.contains(document.activeElement)) more.closest<HTMLElement>(".context-pane")?.focus({ preventScroll: true }); };
  }, []);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!open) return;
    if (event.key === "Escape") {
      // The panel's own Escape, which closes it or goes back, is not for this press.
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
      button.current?.focus();
    } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const items = [...box.current!.querySelectorAll<HTMLElement>("[role^=menuitem]")];
      const at = items.indexOf(document.activeElement as HTMLElement);
      items[(at + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length]?.focus();
    }
  };
  return <div ref={box} className="panel-more" onKeyDown={onKeyDown}>
    <button ref={button} className="icon-button" aria-label="More" aria-haspopup="menu" aria-expanded={open} {...(open ? {} : { "data-tip": "More" })} data-tip-align="end" onClick={() => setOpen(!open)}><Icon name="more" /></button>
    {open && <div className="panel-more-menu" role="menu" aria-label="More">
      {controls.map((control) => <button key={control.id} className="labelled-button" role="menuitem" onClick={() => { setOpen(false); button.current?.focus(); control.onPress(); }}><Icon name={control.icon} /><span>{control.name}</span></button>)}
    </div>}
  </div>;
}
