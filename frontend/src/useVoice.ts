import { useCallback, useEffect, useMemo, useState } from "react";
import { request } from "./api";
import { parseSiqspeak, readDictations, recentMessages, rememberDictation, type SiqspeakReport } from "./voice";

// SIQspeak is asked again this often while its pane is shown.
const REFRESH_MS = 30000;

function store(): Storage | null {
  try { return localStorage; } catch { return null; }
}

// useVoice is one pane's voice bubble: what SIQspeak reports, read through the
// supervisor while the pane is shown and the page visible, and asked again at
// every push-to-talk press, and the pane's own dictations, kept in this
// browser. Nothing is read while the pane is out of sight.
export function useVoice(instance: string, pane: string, shown: boolean) {
  const [report, setReport] = useState<SiqspeakReport | null>(null);
  const [dictations, setDictations] = useState(() => readDictations(store(), pane));
  const [readPane, setReadPane] = useState(pane);
  if (readPane !== pane) { setReadPane(pane); setDictations(readDictations(store(), pane)); }
  const ask = useCallback((signal?: AbortSignal) => request("/api/voice", signal, { method: "POST", headers: { "X-CFO-Token": instance } })
    .then(parseSiqspeak, (): SiqspeakReport => ({ state: "unreadable", messages: [] }))
    .then((next) => { if (!signal?.aborted) setReport(next); return next; }), [instance]);
  useEffect(() => {
    if (!shown) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = () => {
      if (!document.hidden) void ask(controller.signal);
      timer = setTimeout(tick, REFRESH_MS);
    };
    const visible = () => { if (!document.hidden) void ask(controller.signal); };
    tick();
    document.addEventListener("visibilitychange", visible);
    return () => { controller.abort(); clearTimeout(timer); document.removeEventListener("visibilitychange", visible); };
  }, [shown, ask]);
  // SIQspeak owns Ctrl+Shift+Space while it runs.
  const defers = useCallback(() => ask().then((next) => next.state === "running"), [ask]);
  const remember = useCallback((text: string) => setDictations(rememberDictation(store(), pane, text, Date.now())), [pane]);
  const messages = useMemo(() => recentMessages(report?.messages || [], dictations), [report, dictations]);
  return { state: report?.state ?? null, messages, refresh: ask, defers, remember };
}

export type Voice = ReturnType<typeof useVoice>;
