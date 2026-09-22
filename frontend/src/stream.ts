// Adapted from Cline Kanban use-runtime-state-stream.ts, Apache-2.0.
// Copyright 2026 Cline Bot Inc. See public/assets/NOTICE.txt.
// CFO changes: SSE full snapshots, instance/revision fencing, strict parsing,
// bounded retries, visible errors, and no client-driven task progression.
import { useEffect, useState } from "react";
import { parseSnapshot, type Snapshot } from "./types";
import { message } from "./api";

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
  const [connection, setConnection] = useState("Connecting");
  const [error, setError] = useState("");
  useEffect(() => {
    let cancelled = false;
    let source: EventSource | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let retry = STREAM_RECONNECT_BASE_DELAY_MS;
    const connect = () => {
      if (cancelled) return;
      source = new EventSource("/api/events");
      source.addEventListener("snapshot", (event: MessageEvent<string>) => {
        if (cancelled) return;
        try {
          const value: unknown = JSON.parse(event.data);
          const next = parseSnapshot(value);
          setSnapshot((current) => newerSnapshot(current, next));
          setConnection("Live");
          setError("");
          retry = STREAM_RECONNECT_BASE_DELAY_MS;
        } catch (error: unknown) {
          setError(message(error));
          setConnection("Invalid stream");
        }
      });
      source.onerror = () => {
        source?.close();
        if (cancelled) return;
        setConnection("Reconnecting");
        setError(
          "Supervisor connection lost. Showing the last received evidence.",
        );
        timer = setTimeout(connect, retry);
        retry = Math.min(retry * 2, STREAM_RECONNECT_MAX_DELAY_MS);
      };
    };
    connect();
    return () => {
      cancelled = true;
      source?.close();
      clearTimeout(timer);
    };
  }, []);
  return { snapshot, connection, error };
}
