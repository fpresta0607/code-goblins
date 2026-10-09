import { useEffect, useRef, useState } from "react";
import { announce } from "./api";
import { alertItem, announceKey, asksPermission, boardAlerts, NOTIFICATION_CLICK, notifies, outlived, unseen, type BoardAlert, type SeenAlert } from "./alertRules";
import { DialogueBox } from "./DialogueBox";
import { Icon } from "./Icon";
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

// The board's alerts: a Windows notification for each new Command Center item
// that asks the Overlord something, while the board is out of sight, once he
// allows them; a click on it opens that item in the Command Center. On the
// board the bar's Open Command Center, which glows and counts what waits, is
// an item's one signal, so nothing here shows on screen but the one ask for
// notifications, with the first alert. Each item is announced once, in one
// tab of the board: the supervisor hands each alert to the first tab that
// asks, and remembers it through a reload and its own restart. A
// notification leaves when its item is answered or cleared anywhere.
export function Alerts({ snapshot, onOpen }: { snapshot: Snapshot; onOpen: (item: string) => void }) {
  const previous = useRef<Snapshot | null>(null);
  const [stored] = useState(readSeen);
  const seen = useRef<readonly SeenAlert[]>(stored);
  const notes = useRef(new Map<string, { note: Notification; alert: BoardAlert }>());
  const latest = useRef(snapshot);
  const onOpenNow = useRef(onOpen);
  useEffect(() => { onOpenNow.current = onOpen; });
  const [asking, setAsking] = useState(false);
  useEffect(() => {
    const open = (event: Event) => {
      if (event instanceof CustomEvent && typeof event.detail === "string") onOpenNow.current(alertItem(event.detail));
    };
    window.addEventListener(NOTIFICATION_CLICK, open);
    return () => window.removeEventListener(NOTIFICATION_CLICK, open);
  }, []);
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
    // it hands this tab is notified against the items open by then.
    void announce(snapshot.instance, fresh.map(announceKey)).then((claimed) => {
      // With AFK mode on now, even an earlier claim hands nothing.
      const mine = fresh.filter((alert) => latest.current.afk.state !== "on" && (claimed === null || claimed.includes(announceKey(alert))) && !outlived(alert, latest.current));
      if (!mine.length) return;
      if (asksPermission(permission(), asked())) setAsking(true);
      if (!notifies(permission(), document.hidden)) return;
      for (const alert of mine) {
        const note = new Notification(alert.speaker, { body: alert.text, tag: alert.key });
        notes.current.set(alert.key, { note, alert });
        note.onclose = () => { if (notes.current.get(alert.key)?.note === note) notes.current.delete(alert.key); };
        note.onclick = () => {
          window.focus();
          note.close();
          onOpenNow.current(alert.item);
        };
      }
    });
  }, [snapshot]);
  const answer = (allow: boolean) => {
    rememberAsked();
    setAsking(false);
    if (allow && typeof Notification !== "undefined") void Notification.requestPermission();
  };
  if (!asking) return null;
  return <section className="toasts" aria-live="polite" aria-label="Alerts">
    <div className="toast ask">
      <DialogueBox persona="cfo" label="Windows notifications"
        actions={<>
          <button className="pixel-button" onClick={() => answer(true)}>Turn on</button>
          <button className="pixel-button outline" onClick={() => answer(false)}>Not now</button>
          <button className="icon-button pixel-icon" aria-label="Dismiss: Windows notifications" data-tip="Dismiss" onClick={() => answer(false)}><Icon name="close" /></button>
        </>}>
        <p>Show a Windows notification when something needs you while the board is in the background?</p>
      </DialogueBox>
    </div>
  </section>;
}
