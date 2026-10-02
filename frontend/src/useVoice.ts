import { useCallback, useMemo, useState } from "react";
import { readDictations, rememberDictation } from "./voice";

// The list shows this many of the pane's newest dictations.
const RECENT = 5;

function store(): Storage | null {
  try { return localStorage; } catch { return null; }
}

// useVoice is one pane's voice bubble: the pane's own dictations, newest
// first, kept in this browser.
export function useVoice(pane: string) {
  const [dictations, setDictations] = useState(() => readDictations(store(), pane));
  const [readPane, setReadPane] = useState(pane);
  if (readPane !== pane) { setReadPane(pane); setDictations(readDictations(store(), pane)); }
  const remember = useCallback((text: string) => setDictations(rememberDictation(store(), pane, text, Date.now())), [pane]);
  const messages = useMemo(() => dictations.slice(0, RECENT), [dictations]);
  return { messages, remember };
}

export type Voice = ReturnType<typeof useVoice>;
