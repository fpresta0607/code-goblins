import { useEffect, useRef, useState } from "react";
import { DialogueBox } from "./DialogueBox";
import { Icon } from "./Icon";
import type { BoardAlert } from "./alertRules";
import type { Persona } from "./workflow";

// A toast stays this long unless the pointer or the keyboard rests on it.
const TOAST_MS = 8000;

// One alert in the stack, as its goblin's dialogue box: its one action opens
// its item, filled only when it needs the Overlord, and it leaves by itself
// after a while unless the pointer or the keyboard rests on it, or when it is
// dismissed.
export function Toast({ alert, persona, onOpen, onDismiss }: { alert: BoardAlert; persona: Persona; onOpen: () => void; onDismiss: () => void }) {
  const [resting, setResting] = useState(false);
  const dismiss = useRef(onDismiss);
  useEffect(() => { dismiss.current = onDismiss; });
  useEffect(() => {
    if (resting) return;
    const timer = setTimeout(() => dismiss.current(), TOAST_MS);
    return () => clearTimeout(timer);
  }, [resting]);
  return <div className="toast" onPointerEnter={() => setResting(true)} onPointerLeave={() => setResting(false)} onFocus={() => setResting(true)} onBlur={() => setResting(false)}>
    <DialogueBox persona={persona} speaker={alert.speaker} tone={alert.tone} label={alert.text}
      actions={<>
        <button className={"pixel-button" + (alert.tone === "needs" ? "" : " outline")} onClick={onOpen}>{alert.action}</button>
        <button className="icon-button pixel-icon" aria-label={"Dismiss: " + alert.text} data-tip="Dismiss" data-tip-align="end" onClick={onDismiss}><Icon name="close" /></button>
      </>}>
      <p>{alert.text}</p>
    </DialogueBox>
  </div>;
}
