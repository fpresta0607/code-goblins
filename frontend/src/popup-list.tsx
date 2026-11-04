import { useEffect, useLayoutEffect, useRef } from "react";
import "./popup.css";

export interface PopupOption { key: string; label: string; detail?: string }

// POPUP_CLEARANCE_PX is the space the list leaves above it, below whatever
// would cut it off, as it leaves 6px between itself and the box.
const POPUP_CLEARANCE_PX = 6;

// PopupList is the card that pops up over the message box as he types, the
// session's slash commands and skills after a / or a $ and its files after
// an @, on the surface every popup of the chat wears (popup.css): each option
// its name, and below it what it does. It stands above the box, as tall as
// the room between the box and the top of what the screen shows, or of the
// nearest panel that would cut it off, and scrolls inside itself, so a phone's
// keyboard never pushes its first options off the screen. The keyboard stays
// in the box, which moves the pick, takes it and closes the list; the pick is
// kept in view as it moves. A press or a tap on an option takes it without
// taking the box's focus, and a finger that scrolls the list takes none.
export function PopupList({ label, options, pick, onChoose }: { label: string; options: PopupOption[]; pick: number; onChoose: (at: number) => void }) {
  const list = useRef<HTMLUListElement>(null);
  useEffect(() => { list.current?.querySelector<HTMLElement>("[aria-selected=true]")?.scrollIntoView({ block: "nearest" }); }, [pick]);
  useLayoutEffect(() => {
    const element = list.current;
    const box = element?.offsetParent;
    if (!element || !(box instanceof HTMLElement)) return;
    let clipping = box.parentElement;
    while (clipping && getComputedStyle(clipping).overflow === "visible") clipping = clipping.parentElement;
    const viewport = window.visualViewport;
    const measure = () => {
      const top = Math.max(viewport?.offsetTop ?? 0, clipping?.getBoundingClientRect().top ?? 0);
      element.style.setProperty("--popup-room", Math.max(0, Math.floor(box.getBoundingClientRect().top - top - 2 * POPUP_CLEARANCE_PX)) + "px");
    };
    measure();
    const resized = new ResizeObserver(measure);
    resized.observe(box);
    viewport?.addEventListener("resize", measure);
    viewport?.addEventListener("scroll", measure);
    window.addEventListener("resize", measure);
    return () => {
      resized.disconnect();
      viewport?.removeEventListener("resize", measure);
      viewport?.removeEventListener("scroll", measure);
      window.removeEventListener("resize", measure);
    };
  }, []);
  return <ul ref={list} className="popup-surface popup-list" role="listbox" aria-label={label}>
    {options.map((option, at) => <li key={option.key} role="option" aria-selected={at === pick} onMouseDown={(event) => event.preventDefault()} onClick={() => onChoose(at)}>
      <span className="popup-option-label">{option.label}</span>
      {option.detail && <span className="popup-option-detail">{option.detail}</span>}
    </li>)}
  </ul>;
}
