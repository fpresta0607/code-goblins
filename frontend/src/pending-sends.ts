import { useSyncExternalStore } from "react";

// A message he sent that the board has not shown as its own action yet: the
// box adds it the moment he presses Enter, and the conversation draws it at
// once as his queued bubble (the Overlord, 2026-10-27: "im not seeing queued
// messages appear in the modern chat right away, it lags"), until the board's
// snapshot carries the action of the same id or the send fails.
export interface PendingSend { id: string; task: string; text: string }

let sends: PendingSend[] = [];
const listeners = new Set<() => void>();
const changed = () => { for (const listener of listeners) listener(); };

export function addPendingSend(send: PendingSend): void {
  sends = [...sends, send];
  changed();
}

export function removePendingSend(id: string): void {
  if (!sends.some((send) => send.id === id)) return;
  sends = sends.filter((send) => send.id !== id);
  changed();
}

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};

// usePendingSends is every send the board has not shown yet.
export function usePendingSends(): PendingSend[] {
  return useSyncExternalStore(subscribe, () => sends);
}
