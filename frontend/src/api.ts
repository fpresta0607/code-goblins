import { useEffect, useRef, useState } from "react";
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
    );
  return value;
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

// Only a visible native pane refreshes. Each read schedules the next after it
// completes, so a slow Herdr never creates overlapping capture requests.
export function useNativeOutput<T>(path: string | null, parse: (value: unknown) => T) {
  const [result, setResult] = useState<{ path: string; data?: T; error?: string }>({ path: "" });
  const [loading, setLoading] = useState(false);
  const refresh = useRef<() => void>(() => {});
  useEffect(() => {
    if (!path) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let reading = false;
    const read = async () => {
      if (reading || controller.signal.aborted) return;
      clearTimeout(timer);
      if (document.hidden) { timer = setTimeout(() => { void read(); }, 5000); return; }
      reading = true;
      setLoading(true);
      try {
        const data = parse(await request(path, controller.signal));
        if (!controller.signal.aborted) setResult({ path, data });
      } catch (error: unknown) {
        if (!controller.signal.aborted) setResult((prior) => ({ path, data: prior.path === path ? prior.data : undefined, error: message(error) }));
      } finally {
        reading = false;
        if (!controller.signal.aborted) {
          setLoading(false);
          timer = setTimeout(() => { void read(); }, 5000);
        }
      }
    };
    const refreshVisible = () => { void read(); };
    refresh.current = refreshVisible;
    document.addEventListener("visibilitychange", refreshVisible);
    void read();
    return () => { controller.abort(); clearTimeout(timer); document.removeEventListener("visibilitychange", refreshVisible); refresh.current = () => {}; };
  }, [path, parse]);
  return { data: result.path === path ? result.data : undefined, error: result.path === path ? result.error : undefined, loading, reload: () => refresh.current() };
}
