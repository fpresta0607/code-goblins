import { useEffect, useRef, useState } from "react";
import { announce } from "./api";
import { announceKey, arrive, asksPermission, boardAlerts, isItemAlert, notifies, outlived, unseen, type AlertTarget, type BoardAlert, type SeenAlert } from "./alertRules";
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
// asks for that once, with the first alert. Each event shows once on each,
// in one tab of the board: the supervisor hands each alert to the first tab
// that asks, and remembers it through a reload and its own restart. Opening
// or dismissing a toast closes its notification, a toast that only times out
// leaves it, clicking the notification removes the toast and opens its item,
// and both leave when their item is answered or cleared anywhere.
export function Alerts({ snapshot, onOpen }: { snapshot: Snapshot; onOpen: (target: AlertTarget) => void }) {
  const previous = useRef<Snapshot | null>(null);
  const [stored] = useState(readSeen);
  const seen = useRef<readonly SeenAlert[]>(stored);
  const notes = useRef(new Map<string, { note: Notification; alert: BoardAlert }>());
  const latest = useRef(snapshot);
  const onOpenNow = useRef(onOpen);
  useEffect(() => { onOpenNow.current = onOpen; });
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
    latest.current = snapshot;
    for (const { note, alert } of [...notes.current.values()]) if (outlived(alert, snapshot)) note.close();
    const { fresh } = sighting;
    if (!fresh.length) return;
    // The supervisor answers after later snapshots may have come, so what
    // it hands this tab is shown against the items open by then.
    void announce(snapshot.instance, fresh.filter(isItemAlert).map(announceKey), fresh.filter((alert) => !isItemAlert(alert)).map(announceKey)).then((claimed) => {
      const mine = fresh.filter((alert) => (claimed === null || claimed.includes(announceKey(alert))) && !outlived(alert, latest.current));
      if (!mine.length) return;
      setToasts((prior) => arrive(prior, mine));
      if (asksPermission(permission(), asked())) setAsking(true);
      if (!notifies(permission(), document.hidden, document.hasFocus())) return;
      for (const alert of mine) {
        const note = new Notification(alert.speaker, { body: alert.text, tag: alert.key });
        notes.current.set(alert.key, { note, alert });
        note.onclose = () => { if (notes.current.get(alert.key)?.note === note) notes.current.delete(alert.key); };
        note.onclick = () => {
          window.focus();
          note.close();
          setToasts((prior) => prior.filter((toast) => toast.key !== alert.key));
          onOpenNow.current(alert.target);
        };
      }
    });
  }, [snapshot]);
  // A toast leaves with its item, as its notification does.
  if (toasts.some((toast) => outlived(toast, snapshot))) setToasts(toasts.filter((toast) => !outlived(toast, snapshot)));
  const leave = (key: string) => setToasts((prior) => prior.filter((toast) => toast.key !== key));
  const dismiss = (key: string) => { leave(key); notes.current.get(key)?.note.close(); };
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
