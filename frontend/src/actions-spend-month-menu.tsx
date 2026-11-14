import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { Icon } from "./Icon";

function monthName(month: string) {
  return new Date(month + "-01T00:00:00").toLocaleDateString("en-US", { month: "long", year: "numeric" });
}

// The month beside the Actions window's title, a button that opens the months
// the window shows, this month and last month (#2536), the way a panel's
// picture opens its chat styles: the menu opens on the month shown and closes
// on a choice or on Escape, which hand the keyboard back to the month, and on
// a press anywhere else; Up and Down move through it.
export function ActionsSpendMonthMenu({ month, months, onChoose }: { month: string; months: string[]; onChoose: (month: string) => void }) {
  const [isOpen, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!isOpen) return;
    box.current?.querySelector<HTMLElement>("[aria-checked=true]")?.focus();
    const away = (event: PointerEvent) => { if (event.target instanceof Node && !box.current?.contains(event.target)) setOpen(false); };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [isOpen]);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!isOpen) return;
    if (event.key === "Escape") {
      // The window's own Escape, which closes it, is not for this press.
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
  const choose = (chosen: string) => {
    setOpen(false);
    button.current?.focus();
    if (chosen !== month) onChoose(chosen);
  };
  return <div ref={box} className="actions-spend-month" onKeyDown={onKeyDown}>
    <button ref={button} type="button" className="actions-spend-month-button" aria-label={"Month: " + monthName(month)} aria-haspopup="menu" aria-expanded={isOpen} onClick={() => setOpen(!isOpen)}>
      <span>{monthName(month)}</span><Icon name="chevron" />
    </button>
    {isOpen && <div className="panel-more-menu actions-spend-month-menu" role="menu" aria-label="Month">
      {months.map((each) => <button key={each} type="button" className="labelled-button" role="menuitemradio" aria-checked={each === month} onClick={() => choose(each)}><span>{monthName(each)}</span></button>)}
    </div>}
  </div>;
}
