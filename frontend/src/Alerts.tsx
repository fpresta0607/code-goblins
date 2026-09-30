import { useEffect, useRef, useState } from "react";
import { arrive, asksPermission, boardAlerts, notifies, type AlertTarget, type Arrival } from "./alertRules";
import { personaFor } from "./workflow";
import { DialogueBox } from "./DialogueBox";
import { Icon } from "./Icon";
import { Toast } from "./Toast";
import type { Snapshot } from "./types";

// Whether the board already asked this browser for Windows notifications.
const ASKED_KEY = "cfo-notifications-asked";

const permission = (): NotificationPermission | "unsupported" => typeof Notification === "undefined" ? "unsupported" : Notification.permission;
function asked(): boolean {
  try { return localStorage.getItem(ASKED_KEY) === "true"; } catch { return false; }
}
function rememberAsked() {
  try { localStorage.setItem(ASKED_KEY, "true"); } catch { /* the board asks again in another tab */ }
}

// The board's alerts: a stack of toasts at the bottom right for what needs the
// Overlord or finished, each opening its item, and a Windows notification for
// each while he is not looking at the board, once he allows them. The board
// asks for that once, with the first alert.
export function Alerts({ snapshot, onOpen }: { snapshot: Snapshot; onOpen: (target: AlertTarget) => void }) {
  const previous = useRef<Snapshot | null>(null);
  const seen = useRef(new Set<string>());
  const open = useRef(onOpen);
  useEffect(() => { open.current = onOpen; });
  const [toasts, setToasts] = useState<Arrival[]>([]);
  const [asking, setAsking] = useState(false);
  useEffect(() => {
    // A Command Center item alerts once a page; a goblin alerts each time it
    // fails or finishes again, replacing its toast still on screen.
    const fresh = boardAlerts(previous.current, snapshot).filter((alert) => alert.key.startsWith("task:") || !seen.current.has(alert.key));
    previous.current = snapshot;
    if (!fresh.length) return;
    for (const alert of fresh) seen.current.add(alert.key);
    setToasts((prior) => arrive(prior, fresh));
    if (asksPermission(permission(), asked())) setAsking(true);
    if (!notifies(permission(), document.hidden, document.hasFocus())) return;
    for (const alert of fresh) {
      const note = new Notification(alert.speaker, { body: alert.text, tag: alert.key });
      note.onclick = () => { window.focus(); open.current(alert.target); note.close(); };
    }
  }, [snapshot]);
  const dismiss = (id: number) => setToasts((prior) => prior.filter((toast) => toast.id !== id));
  const answer = (allow: boolean) => {
    rememberAsked();
    setAsking(false);
    if (allow && typeof Notification !== "undefined") void Notification.requestPermission();
  };
  if (!toasts.length && !asking) return null;
  return <section className="toasts" aria-live="polite" aria-label="Alerts">
    {asking && <div className="toast ask">
      <DialogueBox persona="cfo" speaker="CFO" tone="needs" label="Windows notifications"
        actions={<>
          <button className="pixel-button" onClick={() => answer(true)}>Turn on</button>
          <button className="pixel-button outline" onClick={() => answer(false)}>Not now</button>
          <button className="icon-button pixel-icon" aria-label="Dismiss: Windows notifications" data-tip="Dismiss" data-tip-align="end" onClick={() => answer(false)}><Icon name="close" /></button>
        </>}>
        <p>Show a Windows notification when something needs you while the board is in the background?</p>
      </DialogueBox>
    </div>}
    {toasts.map(({ id, alert }) => <Toast key={id} alert={alert} persona={alert.task ? personaFor(snapshot.tasks.find((task) => task.id === alert.task)) : "cfo"}
      onOpen={() => { dismiss(id); onOpen(alert.target); }} onDismiss={() => dismiss(id)} />)}
  </section>;
}
