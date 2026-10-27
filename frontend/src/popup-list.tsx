import { useEffect, useRef } from "react";
import "./popup.css";

export interface PopupOption { key: string; label: string; detail?: string }

// PopupList is the list that pops up over the message box as he types, the
// session's slash commands after a / and its files after an @, on the
// surface every popup of the chat wears (popup.css). The keyboard stays in
// the box, which moves the pick, takes it and closes the list; the pick is
// kept in view as it moves, and a press on an option takes it without taking
// the box's focus.
export function PopupList({ label, options, pick, onChoose }: { label: string; options: PopupOption[]; pick: number; onChoose: (at: number) => void }) {
  const list = useRef<HTMLUListElement>(null);
  useEffect(() => { list.current?.querySelector<HTMLElement>("[aria-selected=true]")?.scrollIntoView({ block: "nearest" }); }, [pick]);
  return <ul ref={list} className="popup-surface popup-list" role="listbox" aria-label={label}>
    {options.map((option, at) => <li key={option.key} role="option" aria-selected={at === pick} onMouseDown={(event) => { event.preventDefault(); onChoose(at); }}>
      <span className="popup-option-label">{option.label}</span>
      {option.detail && <span className="popup-option-detail">{option.detail}</span>}
    </li>)}
  </ul>;
}
