import { useEffect, useState } from "react";
import { object, string } from "./types";

export async function request(
  path: string,
  signal?: AbortSignal,
  init?: RequestInit,
): Promise<unknown> {
  const response = await fetch(path, { ...init, signal });
  const value: unknown = await response.json();
  if (!response.ok)
    throw new Error(
      string(object(value).error) || `Request failed (${response.status})`,
      { cause: value },
    );
  return value;
}
// A tab he cannot see waits this long before it asks the supervisor, so a tab
// he is looking at claims first and shows the alert and the Command Center
// where he sees them. With no tab in view the hidden one claims after the
// wait, so a board in the background still sends its Windows notification.
const HIDDEN_TAB_WAIT_MS = 1500;

// announce asks the supervisor which of these the board has not announced
// yet, and gets back those it may announce now with a Windows notification.
// The supervisor hands each key to one request, so no item is announced
// twice, in another tab, after a reload or after the supervisor restarts.
// Null means the supervisor could not be asked, and the caller falls back on
// what this browser remembers.
export async function announce(instance: string, keys: string[]): Promise<string[] | null> {
  if (!keys.length) return [];
  if (document.hidden) await new Promise((resolve) => setTimeout(resolve, HIDDEN_TAB_WAIT_MS));
  try {
    const { claimed } = object(await request("/api/announce", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ keys }) }));
    return Array.isArray(claimed) ? claimed.filter((key): key is string => typeof key === "string") : null;
  } catch { return null; }
}
// The supervisor instance this board talks to, from its latest snapshot: a
// report to the CFO carries it, as every board action does.
let instance = "";
export function knowInstance(id: string): void { instance = id; }

// reportToCfo gives the CFO a failure only this board saw, such as a
// clipboard or a microphone it could not use or a read that failed, rather
// than showing it to the Overlord: the Overlord, 2026-10-08, "everything error
// wise goes to cfo and cfo decides what to tell me in command center". A
// refusal of his own click is said beside it for a moment (ClickFeedback),
// and the supervisor's own errors reach the CFO from the supervisor. A
// report that cannot be sent has nowhere else to go.
export function reportToCfo(where: string, text: string): void {
  if (!instance || !text.trim()) return;
  fetch("/api/cfo/report", { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ where, text }) }).catch(() => undefined);
}

export function message(error: unknown): string {
  return error instanceof Error ? error.message : "Request failed";
}
export function useResource<T>(
  path: string | null,
  parse: (value: unknown) => T,
) {
  const [result, setResult] = useState<{
    path: string;
    data?: T;
    error?: string;
  }>({ path: "" });
  const [version, setVersion] = useState(0);
  useEffect(() => {
    if (!path) return;
    const controller = new AbortController();
    request(path, controller.signal)
      .then(parse)
      .then((data) => {
        if (!controller.signal.aborted) setResult({ path, data });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        reportToCfo("reading " + path, message(error));
        setResult({ path, error: message(error) });
      });
    return () => controller.abort();
  }, [path, parse, version]);
  return {
    data: path === result.path ? result.data : undefined,
    error: path === result.path ? result.error : undefined,
    reload: () => {
      setResult({ path: "" });
      setVersion((v) => v + 1);
    },
  };
}
