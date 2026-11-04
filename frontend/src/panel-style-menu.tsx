import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import type { ChatStyle } from "./chat-style";
import { Icon, type IconName } from "./Icon";

// The chat style a panel's picture offers: the style shown, and what a choice
// of the other does.
export interface StyleChoice { shown: ChatStyle; onChoose: (style: ChatStyle) => void }

// The two ways a conversation is drawn, as the menu names them.
const STYLES: { style: ChatStyle; name: string; icon: IconName }[] = [
  { style: "modern", name: "Modern chat", icon: "comment" },
  { style: "classic", name: "Terminal", icon: "terminal" },
];

// The session's picture in its panel's header, a button that opens the
// choice of chat style (the Overlord, 2026-11-03: "when you click on cfo
// avatar the dropdown with both options"; #1987). The menu opens on the style
// shown and closes on a choice or on Escape, which hand the keyboard back to
// the picture, and on a press anywhere else; Up and Down move through it. A
// choice of the style already shown changes nothing. The menu hangs from the
// header, not from the picture's part of the row, which cuts what overflows
// it.
export function PanelStyleMenu({ picture, shown, onChoose }: StyleChoice & { picture: ReactNode }) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    box.current?.querySelector<HTMLElement>("[aria-checked=true]")?.focus();
    const away = (event: PointerEvent) => { if (event.target instanceof Node && !box.current?.contains(event.target)) setOpen(false); };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);
  useLayoutEffect(() => {
    const menu = box.current;
    return () => { if (menu?.contains(document.activeElement)) menu.closest<HTMLElement>(".context-pane")?.focus({ preventScroll: true }); };
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
      const items = [...box.current!.querySelectorAll<HTMLElement>("[role=menuitemradio]")];
      const at = items.indexOf(document.activeElement as HTMLElement);
      items[(at + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length]?.focus();
    }
  };
  const choose = (style: ChatStyle) => {
    setOpen(false);
    button.current?.focus();
    if (style !== shown) onChoose(style);
  };
  return <div ref={box} className="panel-style" onKeyDown={onKeyDown}>
    <button ref={button} className="panel-style-button" aria-label="Chat style" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>{picture}</button>
    {open && <div className="panel-more-menu panel-style-menu" role="menu" aria-label="Chat style">
      {STYLES.map((each) => <button key={each.style} className="labelled-button" role="menuitemradio" aria-checked={each.style === shown} onClick={() => choose(each.style)}><Icon name={each.icon} /><span>{each.name}</span></button>)}
    </div>}
  </div>;
}
