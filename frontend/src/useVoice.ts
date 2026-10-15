import { useSyncExternalStore } from "react";
import { request } from "./api";
import { parseDictations, type VoiceMessage } from "./voice";

// The list shows this many of the board's newest dictations.
const RECENT = 5;

// The board's recent dictations as this page last had them, and the voice
// bubbles showing them. The supervisor keeps the one list every window reads.
let messages: VoiceMessage[] = [];
const watchers = new Set<() => void>();

function show(next: VoiceMessage[]): void {
  messages = next.slice(0, RECENT);
  for (const watcher of watchers) watcher();
}

function watch(watcher: () => void): () => void {
  watchers.add(watcher);
  return () => { watchers.delete(watcher); };
}

// refreshDictations reads the list again, with what other windows added. A
// list that cannot be read, as on a board reached from another PC, stays as
// this page has it.
export function refreshDictations(): void {
  request("/api/dictations").then((value) => show(parseDictations(value))).catch(() => undefined);
}

// rememberDictation shows text first in this page at once and has the
// supervisor keep it for every window, as the board whose token is instance.
// It keeps the words alone, without the space typed before a dictation that
// followed another. One the supervisor refuses is shown for this visit only.
export function rememberDictation(text: string, instance: string): void {
  const words = text.trim();
  show([{ text: words, at: Date.now() }, ...messages]);
  request("/api/dictations", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ text: words }) })
    .then((value) => show(parseDictations(value))).catch(() => undefined);
}

// useVoice is the board's recent dictations, newest first.
export function useVoice(): VoiceMessage[] {
  return useSyncExternalStore(watch, () => messages);
}
