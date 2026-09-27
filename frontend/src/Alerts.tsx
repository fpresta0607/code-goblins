import { useEffect, useRef, useState } from "react";
import { asksPermission, boardAlerts, notifies, type AlertTarget, type BoardAlert } from "./alertRules";
import { personaFor } from "./workflow";
import { Toast } from "./Toast";
import type { Snapshot } from "./types";

// The most toasts shown at once; the oldest leaves first.
const MAX_TOASTS = 4;
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
  const [toasts, setToasts] = useState<BoardAlert[]>([]);
  const [asking, setAsking] = useState(false);
  useEffect(() => {
    const fresh = boardAlerts(previous.current, snapshot).filter((alert) => !seen.current.has(alert.key));
    previous.current = snapshot;
    if (!fresh.length) return;
    for (const alert of fresh) seen.current.add(alert.key);
    setToasts((prior) => [...prior, ...fresh].slice(-MAX_TOASTS));
    if (asksPermission(permission(), asked())) setAsking(true);
    if (!notifies(permission(), document.hidden, document.hasFocus())) return;
    for (const alert of fresh) {
      const note = new Notification(alert.title, { body: alert.text, tag: alert.key });
      note.onclick = () => { window.focus(); open.current(alert.target); note.close(); };
    }
  }, [snapshot]);
  const dismiss = (key: string) => setToasts((prior) => prior.filter((alert) => alert.key !== key));
  const answer = (allow: boolean) => {
    rememberAsked();
    setAsking(false);
    if (allow && typeof Notification !== "undefined") void Notification.requestPermission();
  };
  if (!toasts.length && !asking) return null;
  return <section className="toasts" aria-live="polite" aria-label="Alerts">
    {asking && <div className="toast ask" role="group" aria-label="Windows notifications">
      <p>Show a Windows notification when something needs you while the board is in the background?</p>
      <div className="toast-actions"><button onClick={() => answer(false)}>Not now</button><button className="primary" onClick={() => answer(true)}>Allow</button></div>
    </div>}
    {toasts.map((alert) => <Toast key={alert.key} alert={alert} persona={alert.task ? personaFor(snapshot.tasks.find((task) => task.id === alert.task)) : "cfo"}
      onOpen={() => { dismiss(alert.key); onOpen(alert.target); }} onDismiss={() => dismiss(alert.key)} />)}
  </section>;
}
