import { useEffect, useRef, useState } from "react";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import type { BoardAlert } from "./alertRules";
import type { Persona } from "./workflow";

// A toast stays this long unless the pointer or the keyboard rests on it.
const TOAST_MS = 8000;
// Its fade out lasts this long before it leaves the stack.
const FADE_MS = 220;

// One alert in the stack: clicking it opens its item, it fades by itself
// after a while unless the pointer or the keyboard rests on it, and it can be
// dismissed.
export function Toast({ alert, persona, onOpen, onDismiss }: { alert: BoardAlert; persona: Persona; onOpen: () => void; onDismiss: () => void }) {
  const [resting, setResting] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const dismiss = useRef(onDismiss);
  useEffect(() => { dismiss.current = onDismiss; });
  useEffect(() => {
    if (leaving) {
      const timer = setTimeout(() => dismiss.current(), FADE_MS);
      return () => clearTimeout(timer);
    }
    if (resting) return;
    const timer = setTimeout(() => setLeaving(true), TOAST_MS);
    return () => clearTimeout(timer);
  }, [resting, leaving]);
  return <div className={"toast " + alert.tone + (leaving ? " leaving" : "")} onPointerEnter={() => setResting(true)} onPointerLeave={() => setResting(false)} onFocus={() => setResting(true)} onBlur={() => setResting(false)}>
    <button className="toast-open" onClick={onOpen}>
      <Avatar persona={persona} small />
      <span className="toast-text"><strong>{alert.title}</strong><span>{alert.text}</span></span>
    </button>
    <button className="icon-button" aria-label={"Dismiss: " + alert.title} data-tip="Dismiss" data-tip-align="end" onClick={() => setLeaving(true)}><Icon name="close" /></button>
  </div>;
}
