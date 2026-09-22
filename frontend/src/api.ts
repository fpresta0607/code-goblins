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
