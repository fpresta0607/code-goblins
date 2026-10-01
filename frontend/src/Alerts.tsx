import { useEffect, useRef, useState } from "react";
import { arrive, asksPermission, boardAlerts, notifies, unseen, type AlertTarget, type BoardAlert, type SeenAlert } from "./alertRules";
import { personaFor } from "./workflow";
import { DialogueBox } from "./DialogueBox";
import { Icon } from "./Icon";
import { Toast } from "./Toast";
import type { Snapshot } from "./types";

// Whether the board already asked this browser for Windows notifications.
const ASKED_KEY = "cfo-notifications-asked";
// The alerts this browser has shown, so a reload never shows one again.
const SEEN_KEY = "cfo-alerts-seen-v1";

const permission = (): NotificationPermission | "unsupported" => typeof Notification === "undefined" ? "unsupported" : Notification.permission;
function asked(): boolean {
  try { return localStorage.getItem(ASKED_KEY) === "true"; } catch { return false; }
}
function rememberAsked() {
  try { localStorage.setItem(ASKED_KEY, "true"); } catch { /* the board asks again in another tab */ }
}
const isSeenAlert = (one: unknown): one is SeenAlert => typeof one === "object" && one !== null && "key" in one && typeof one.key === "string" && "says" in one && typeof one.says === "string" && "at" in one && typeof one.at === "number";
function readSeen(): SeenAlert[] {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(SEEN_KEY) || "[]");
    return Array.isArray(saved) ? saved.filter(isSeenAlert) : [];
  } catch { return []; }
}
function rememberSeen(seen: readonly SeenAlert[]) {
  try { localStorage.setItem(SEEN_KEY, JSON.stringify(seen)); } catch { /* this page still shows each alert once */ }
}

// The board's alerts: a stack of toasts at the bottom right for what needs the
// Overlord or finished, each opening its item, and a Windows notification for
// each while he is not looking at the board, once he allows them. The board
// asks for that once, with the first alert. Each event shows once on each:
// opening or dismissing it on one leaves the other, and clicking its Windows
// notification opens the toast's own item.
export function Alerts({ snapshot, onOpen }: { snapshot: Snapshot; onOpen: (target: AlertTarget) => void }) {
  const previous = useRef<Snapshot | null>(null);
  const [stored] = useState(readSeen);
  const seen = useRef<readonly SeenAlert[]>(stored);
  const notes = useRef(new Map<string, Notification>());
  const open = useRef(onOpen);
  useEffect(() => { open.current = onOpen; });
  const [toasts, setToasts] = useState<BoardAlert[]>([]);
  const [asking, setAsking] = useState(false);
  useEffect(() => {
    const now = Date.now();
    const alerts = boardAlerts(previous.current, snapshot);
    const sighting = unseen(alerts, seen.current, now);
    previous.current = snapshot;
    if (sighting.seen !== seen.current) {
      seen.current = sighting.seen;
      // Another tab of the board remembers its own alerts in the same list.
      rememberSeen(unseen(alerts, readSeen(), now).seen);
    }
    const { fresh } = sighting;
    if (!fresh.length) return;
    setToasts((prior) => arrive(prior, fresh));
    if (asksPermission(permission(), asked())) setAsking(true);
    if (!notifies(permission(), document.hidden, document.hasFocus())) return;
    for (const alert of fresh) {
      const note = new Notification(alert.speaker, { body: alert.text, tag: alert.key });
      notes.current.set(alert.key, note);
      note.onclose = () => { if (notes.current.get(alert.key) === note) notes.current.delete(alert.key); };
      note.onclick = () => {
        window.focus();
        note.close();
        setToasts((prior) => prior.filter((toast) => toast.key !== alert.key));
        open.current(alert.target);
      };
    }
  }, [snapshot]);
  const leave = (key: string) => setToasts((prior) => prior.filter((toast) => toast.key !== key));
  const dismiss = (key: string) => { leave(key); notes.current.get(key)?.close(); };
  const answer = (allow: boolean) => {
    rememberAsked();
    setAsking(false);
    if (allow && typeof Notification !== "undefined") void Notification.requestPermission();
  };
  if (!toasts.length && !asking) return null;
  return <section className="toasts" aria-live="polite" aria-label="Alerts">
    {asking && <div className="toast ask">
      <DialogueBox persona="cfo" tone="needs" label="Windows notifications"
        actions={<>
          <button className="pixel-button" onClick={() => answer(true)}>Turn on</button>
          <button className="pixel-button outline" onClick={() => answer(false)}>Not now</button>
          <button className="icon-button pixel-icon" aria-label="Dismiss: Windows notifications" data-tip="Dismiss" data-tip-align="end" onClick={() => answer(false)}><Icon name="close" /></button>
        </>}>
        <p>Show a Windows notification when something needs you while the board is in the background?</p>
      </DialogueBox>
    </div>}
    {toasts.map((alert) => <Toast key={alert.key} alert={alert} persona={alert.task ? personaFor(snapshot.tasks.find((task) => task.id === alert.task)) : "cfo"}
      onOpen={() => { dismiss(alert.key); onOpen(alert.target); }} onDismiss={() => dismiss(alert.key)} onExpire={() => leave(alert.key)} />)}
  </section>;
}
