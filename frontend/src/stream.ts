// Adapted from Cline Kanban use-runtime-state-stream.ts, Apache-2.0.
// Copyright 2026 Cline Bot Inc. See public/assets/NOTICE.txt.
// CFO changes: SSE snapshots and their changes, instance/revision fencing,
// strict parsing, bounded retries, errors given to the CFO, and no
// client-driven task progression.
// After its first snapshot the supervisor sends only changes to it, the
// Command Center's items the moment one changes among them, which the board
// applies to the snapshot it holds, and each build's time, revision and meters
// apart.
import { useEffect, useState } from "react";
import { object, parseReadings, parseSnapshot, type Snapshot } from "./types";
import { knowInstance, message, reportToCfo } from "./api";
import { applyChange } from "./snapshot-change";
import { shared } from "./shared";

const STREAM_RECONNECT_BASE_DELAY_MS = 500;
const STREAM_RECONNECT_MAX_DELAY_MS = 5_000;
export function newerSnapshot(
  current: Snapshot | null,
  next: Snapshot,
): Snapshot {
  return current?.instance === next.instance && current.revision > next.revision
    ? current
    : next;
}
export function useRuntimeStream() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [receivedAt, setReceivedAt] = useState(0);
  const [connection, setConnection] = useState("Connecting");
  useEffect(() => {
    let cancelled = false;
    let source: EventSource | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let retry = STREAM_RECONNECT_BASE_DELAY_MS;
    const reconnect = () => {
      source?.close();
      if (cancelled) return;
      setConnection("Reconnecting");
      timer = setTimeout(connect, retry);
      retry = Math.min(retry * 2, STREAM_RECONNECT_MAX_DELAY_MS);
    };
    const connect = () => {
      if (cancelled) return;
      // held is this connection's snapshot as the supervisor sent it, with
      // each change since applied, which the next change goes on.
      let held: unknown;
      const take = (value: unknown) => {
        const next = parseSnapshot(value);
        knowInstance(next.instance);
        setSnapshot((current) => {
          const newer = newerSnapshot(current, next);
          return newer === next ? shared(current, next) : newer;
        });
        setReceivedAt(Date.now());
        setConnection("Live");
        retry = STREAM_RECONNECT_BASE_DELAY_MS;
      };
      source = new EventSource("/api/events");
      source.addEventListener("snapshot", (event: MessageEvent<string>) => {
        if (cancelled) return;
        try {
          held = JSON.parse(event.data);
          take(held);
        } catch (error: unknown) {
          reportToCfo("the board's snapshot stream", message(error));
          setConnection("Invalid stream");
        }
      });
      source.addEventListener("change", (event: MessageEvent<string>) => {
        if (cancelled) return;
        try {
          held = applyChange(held, JSON.parse(event.data));
          take(held);
        } catch (error: unknown) {
          // What the board holds no longer matches what the stream goes on
          // from, so it opens the stream again for a whole snapshot.
          reportToCfo("the board's snapshot stream", message(error));
          reconnect();
        }
      });
      // A reading, the snapshot's time, revision and meters, is laid over the
      // snapshot the board shows without parsing the fleet it holds again. It
      // comes after the change of its build, so a click waiting for that
      // revision sees the change first.
      source.addEventListener("meters", (event: MessageEvent<string>) => {
        if (cancelled) return;
        try {
          held = applyChange(held, JSON.parse(event.data));
          const readings = parseReadings(object(held));
          setSnapshot((current) => current && shared(current, { ...current, ...readings }));
          setReceivedAt(Date.now());
        } catch (error: unknown) {
          reportToCfo("the board's snapshot stream", message(error));
          reconnect();
        }
      });
      source.onerror = reconnect;
    };
    connect();
    return () => {
      cancelled = true;
      source?.close();
      clearTimeout(timer);
    };
  }, []);
  return { snapshot, connection, receivedAt };
}
