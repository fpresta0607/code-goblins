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
// yet, and gets back those it may announce now: an alert, a Windows
// notification, the Command Center opening by itself. The supervisor hands
// each key to one request, so no item is announced twice, in another tab,
// after a reload or after the supervisor restarts. keys are items, announced
// once; news is a goblin's news, which is a new event when the same words
// come again later. Null means the supervisor could not be asked, and the
// caller falls back on what this browser remembers.
export async function announce(instance: string, keys: string[], news: string[] = []): Promise<string[] | null> {
  if (!keys.length && !news.length) return [];
  if (document.hidden) await new Promise((resolve) => setTimeout(resolve, HIDDEN_TAB_WAIT_MS));
  try {
    const { claimed } = object(await request("/api/announce", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ keys, news }) }));
    return Array.isArray(claimed) ? claimed.filter((key): key is string => typeof key === "string") : null;
  } catch { return null; }
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
        if (!controller.signal.aborted)
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
