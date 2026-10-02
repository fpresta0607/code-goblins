import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Item } from "./commandQueue";
import { holdClosed, remembered, type Sent } from "./item-state";
import { object, string, type Snapshot } from "./types";

// The channel on which the boards open in one browser tell each other what
// the Overlord sent, so each closes the item in the same frame.
const CHANNEL = "cfo-command-center-sent";
// How many sends a board remembers, newest last.
const SENT_LIMIT = 200;

// useItemState is the snapshot the board draws: the one it received, with
// every item it knows to be closed held closed (see holdClosed). sent takes
// what the Overlord sent for an item the moment he sends it, and null when the
// supervisor refused it, which puts the item back; every other board open in
// this browser hears both.
export function useItemState(received: Snapshot | null): { snapshot: Snapshot | null; sent: (key: string, what: Sent | null) => void } {
  const [memory, setMemory] = useState<{ of: Snapshot | null; closed: ReadonlyMap<string, Item> }>({ of: null, closed: new Map() });
  const [sends, setSends] = useState<ReadonlyMap<string, Sent>>(new Map());
  const channel = useRef<BroadcastChannel | null>(null);
  if (received && memory.of !== received) setMemory({ of: received, closed: remembered(memory.closed, received) });
  const record = useCallback((key: string, what: Sent | null) => setSends((prior) => {
    const next = new Map(prior);
    next.delete(key);
    if (what) next.set(key, what);
    for (const oldest of next.keys()) {
      if (next.size <= SENT_LIMIT) break;
      next.delete(oldest);
    }
    return next;
  }), []);
  useEffect(() => {
    if (typeof BroadcastChannel === "undefined") return;
    const opened = new BroadcastChannel(CHANNEL);
    channel.current = opened;
    opened.onmessage = (event: MessageEvent<unknown>) => {
      try {
        const told = object(event.data);
        const what = told.sent == null ? null : object(told.sent);
        record(string(told.key), what && { kind: string(what.kind), id: string(what.id), text: string(what.text), answer_kind: string(what.answer_kind) });
      } catch { /* a board of another build said something this one does not read */ }
    };
    return () => { opened.close(); channel.current = null; };
  }, [record]);
  const sent = useCallback((key: string, what: Sent | null) => {
    record(key, what);
    channel.current?.postMessage({ key, sent: what });
  }, [record]);
  const snapshot = useMemo(() => received && holdClosed(received, memory.closed, sends), [received, memory.closed, sends]);
  return { snapshot, sent };
}
