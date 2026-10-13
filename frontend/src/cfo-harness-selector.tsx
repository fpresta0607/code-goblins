import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { ConnectorMark } from "./ConnectorMark";
import { harnessMark } from "./connectors";
import { Icon } from "./Icon";

const HARNESSES = [{ id: "claude", name: "Claude Code" }, { id: "codex", name: "Codex" }, { id: "pi", name: "pi" }];

export function CfoHarnessSelector({ harness, isDisabled, onSelect }: { harness: string; isDisabled: boolean; onSelect: (harness: string) => void }) {
  const [isOpen, setOpen] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const menuId = useId();
  const name = HARNESSES.find((choice) => choice.id === harness)?.name || harness;
  useEffect(() => {
    if (!isOpen) return;
    container.current?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus();
    const away = (event: PointerEvent) => { if (event.target instanceof Node && !container.current?.contains(event.target)) setOpen(false); };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [isOpen]);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!isOpen) {
      if (!isDisabled && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
        event.preventDefault();
        setOpen(true);
      }
      return;
    }
    if (event.key === "Escape" || event.key === "Tab") {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
      }
      setOpen(false);
      button.current?.focus();
    } else if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const items = [...(container.current?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? [])];
      const current = items.findIndex((item) => item === document.activeElement);
      const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (current + (event.key === "ArrowDown" ? 1 : items.length - 1)) % items.length;
      items[next]?.focus();
    }
  };
  return <div ref={container} className="cfo-harness-selector" onKeyDown={onKeyDown} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}>
    <button ref={button} className="labelled-button cfo-harness-trigger" aria-label={`Harness: ${name}`} aria-haspopup="menu" aria-expanded={isOpen} aria-controls={isOpen ? menuId : undefined} disabled={isDisabled} onClick={() => setOpen(!isOpen)}>
      <ConnectorMark mark={harnessMark(harness)} label={name} /><span>{name}</span><Icon name="chevron" />
    </button>
    {isOpen && <div id={menuId} className="cfo-harness-menu" role="menu" aria-label="Harness">
      {HARNESSES.map((choice) => <button key={choice.id} className="labelled-button" role="menuitemradio" aria-label={choice.name} aria-checked={harness === choice.id} tabIndex={-1} onClick={() => { onSelect(choice.id); setOpen(false); button.current?.focus(); }}>
        <ConnectorMark mark={harnessMark(choice.id)} label={choice.name} /><span>{choice.name}</span>
      </button>)}
    </div>}
  </div>;
}
