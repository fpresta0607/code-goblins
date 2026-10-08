import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { cardPlace, type Box, type Side } from "./quick-tour";
import "./quick-tour.css";

interface Step { part: string; side: Side; title: string; text: ReactNode }

// Each step lights one part of the board, found by its selector, and the CFO
// says one line about it from the side named.
const STEPS: Step[] = [
  { part: ".context-pane", side: "left", title: "I'm the CFO. Tell me what to build.", text: "Type in my terminal, or hold Ctrl+Shift+Space and speak." },
  { part: ".canvas-region", side: "right", title: "Goblins do the work.", text: "Each card is one job. Click a card to watch it." },
  { part: ".command-center-menu > summary", side: "under", title: "When I need you, it waits here.",
    text: <>Replay this tour with <span className="quick-tour-icon"><Icon name="question" /></span><span className="sr-only">the question mark button</span>.</> },
];

interface Lit { part: Box | null; radius: string; card: { left: number; top: number } }

// The quick tour: in three steps the CFO shows a new user how Code Goblins
// works, lighting one part of the board at a time and dimming the rest. It is
// a modal dialog, so nothing behind it takes the keyboard while it shows, and
// X or Escape ends it at once. A part out of sight, such as the board behind
// a maximized panel, lights nothing, and its card sits at the window's foot.
export function QuickTourDialog({ onEnd }: { onEnd: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const next = useRef<HTMLButtonElement>(null);
  const [at, setAt] = useState(0);
  const [lit, setLit] = useState<Lit | null>(null);
  const title = useId(), text = useId();
  const step = STEPS[at];
  const last = at === STEPS.length - 1;
  // Shown before the card is measured, since a closed dialog lays out nothing.
  useLayoutEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  useEffect(() => next.current?.focus(), [at]);
  // The light and the card follow the part as the window, the page's scroll
  // and the board change.
  useLayoutEffect(() => {
    const part = document.querySelector<HTMLElement>(step.part);
    const measure = () => {
      const box = part?.getBoundingClientRect();
      const shown = box && box.width > 0 && box.height > 0 ? { left: box.left, top: box.top, width: box.width, height: box.height } : null;
      const view = { width: document.documentElement.clientWidth, height: document.documentElement.clientHeight };
      setLit({ part: shown, radius: part ? getComputedStyle(part).borderRadius : "", card: cardPlace(shown, card.current!.getBoundingClientRect(), view, step.side) });
    };
    part?.scrollIntoView({ block: "nearest" });
    measure();
    const observer = new ResizeObserver(measure);
    if (part) observer.observe(part);
    observer.observe(card.current!);
    addEventListener("resize", measure);
    addEventListener("scroll", measure, true);
    return () => { observer.disconnect(); removeEventListener("resize", measure); removeEventListener("scroll", measure, true); };
  }, [step]);
  return <dialog ref={dialog} className={"quick-tour" + (lit && !lit.part ? " unlit" : "")} aria-labelledby={title} aria-describedby={text}
    onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onEnd(); }}>
    {lit?.part && <div className="quick-tour-light" style={{ left: lit.part.left, top: lit.part.top, width: lit.part.width, height: lit.part.height, borderRadius: lit.radius }} />}
    <div key={at} ref={card} className="dialogue quick-tour-card" style={lit ? { left: lit.card.left, top: lit.card.top } : undefined}>
      <div className="dialogue-box">
        <span className="dialogue-portrait"><Avatar persona="cfo" /></span>
        <div className="dialogue-text">
          <h2 id={title} className="quick-tour-title">{step.title}</h2>
          <p id={text}>{step.text}</p>
        </div>
        <div className="dialogue-actions">
          <span className="quick-tour-steps" role="img" aria-label={`Step ${at + 1} of ${STEPS.length}`}>{STEPS.map((_, index) => <span key={index} className={index === at ? "now" : ""} />)}</span>
          <button ref={next} className="pixel-button" onClick={() => { if (last) onEnd(); else setAt(at + 1); }}>{last ? "Done" : "Next"}</button>
        </div>
        <button className="icon-button pixel-icon quick-tour-skip" aria-label="Skip the tour" data-tip="Skip the tour" data-tip-align="end" onClick={onEnd}><Icon name="close" /></button>
      </div>
    </div>
  </dialog>;
}
