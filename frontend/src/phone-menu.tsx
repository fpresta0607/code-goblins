import { useEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode } from "react";
import { Icon } from "./Icon";
import "./phone-menu.css";

// The "..." of a panel's one-row header on a phone, Modern Chat's own menu
// (the Overlord, 2026-10-28, of Claude's phone app: "This is the mobile layout
// we need to emulate"): what the header and the box show on a wider screen
// and a phone has no room for. A press outside it or Escape closes it, and so
// does a choice in it, but not a switch it holds, such as AFK, whose state
// he watches change.
export function PhoneMenu({ children }: { children: ReactNode }) {
  const [isOpen, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const sheet = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!isOpen) return;
    const away = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!sheet.current?.contains(target) && !button.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [isOpen]);
  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "Escape") return;
    event.stopPropagation();
    setOpen(false);
    button.current?.focus();
  };
  const chosen = (event: MouseEvent<HTMLDivElement>) => {
    const target = event.target as Element;
    if (target.closest("button, a") && !target.closest(".afk-toggle")) setOpen(false);
  };
  return <div className="phone-menu">
    <button ref={button} type="button" className="phone-menu-button" aria-label="More" aria-haspopup="true" aria-expanded={isOpen} onClick={() => setOpen((prior) => !prior)}><Icon name="more" /></button>
    {isOpen && <div ref={sheet} className="popup-surface phone-menu-sheet" role="group" aria-label="More" onKeyDown={keys} onClick={chosen}>{children}</div>}
  </div>;
}
