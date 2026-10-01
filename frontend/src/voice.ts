import { array, object, string } from "./types.ts";

// What a terminal's voice bubble shows: SIQspeak, the Overlord's own local
// dictation app, as the supervisor's read-only /api/voice reports it (whether
// it is installed and running, and its newest transcriptions), beside the
// board's own push-to-talk dictations for that pane, which stay in this
// browser only. The board never stores, caches or logs SIQspeak's words.

export type SiqspeakState = "running" | "stopped" | "missing" | "unreadable";
export interface VoiceMessage { text: string; at: number; source: "siqspeak" | "board" }
export interface SiqspeakReport { state: SiqspeakState; messages: VoiceMessage[] }

const UNREADABLE: SiqspeakReport = { state: "unreadable", messages: [] };
const STATES: SiqspeakState[] = ["running", "stopped", "missing"];
const time = (value: unknown) => typeof value === "number" && Number.isFinite(value) ? value : null;

export function parseSiqspeak(value: unknown): SiqspeakReport {
  try {
    const report = object(value);
    const state = STATES.find((each) => each === report.state);
    if (!state) return UNREADABLE;
    const messages = array(report.entries).flatMap((raw) => {
      const entry = object(raw), text = string(entry.text), seconds = time(entry.time_epoch);
      return text.trim() ? [{ text, at: seconds === null ? 0 : Math.round(seconds * 1000), source: "siqspeak" as const }] : [];
    });
    return { state, messages };
  } catch { return UNREADABLE; }
}

// The board keeps its own newest BOARD_DICTATIONS per pane in this browser,
// for the BOARD_PANES panes used most recently, so goblins that come and go
// never grow what it keeps.
export const BOARD_DICTATIONS = 10;
export const BOARD_PANES = 20;
const DICTATIONS_KEY = "cfo-dictations-v1";
type Store = Pick<Storage, "getItem" | "setItem">;
type Kept = Record<string, VoiceMessage[]>;

// A pane whose record is broken reads as none, leaving the other panes be.
function parseKept(entries: unknown): VoiceMessage[] {
  try {
    return array(entries).flatMap((raw) => {
      try {
        const entry = object(raw), text = string(entry.text), at = time(entry.at);
        return text && at !== null ? [{ text, at, source: "board" as const }] : [];
      } catch { return []; }
    }).slice(0, BOARD_DICTATIONS);
  } catch { return []; }
}

function readKept(storage: Store | null): Kept {
  try {
    const saved = object(JSON.parse(storage?.getItem(DICTATIONS_KEY) || "{}"));
    return Object.fromEntries(Object.entries(saved).map(([pane, entries]) => [pane, parseKept(entries)]));
  } catch { return {}; }
}

export function readDictations(storage: Store | null, pane: string): VoiceMessage[] {
  return readKept(storage)[pane] || [];
}

// rememberDictation returns the pane's dictations with this one first, even
// when the browser refuses to keep them.
export function rememberDictation(storage: Store | null, pane: string, text: string, at: number): VoiceMessage[] {
  const kept = readKept(storage);
  const messages = [{ text, at, source: "board" as const }, ...(kept[pane] || [])].slice(0, BOARD_DICTATIONS);
  const panes = Object.entries({ ...kept, [pane]: messages })
    .sort(([, a], [, b]) => (b[0]?.at ?? 0) - (a[0]?.at ?? 0)).slice(0, BOARD_PANES);
  try {
    storage?.setItem(DICTATIONS_KEY, JSON.stringify(Object.fromEntries(panes.map(([each, entries]) => [each, entries.map(({ text, at }) => ({ text, at }))]))));
  } catch { /* shown for this visit only */ }
  return messages;
}

// hostPane is the pane a native host's query names: its goblin's task, the
// same key a Herdr pane uses, so a relaunched goblin keeps its dictations, or
// the CFO.
export function hostPane(query: string): string {
  return new URLSearchParams(query).get("task") || "cfo";
}

// The list shows this many of the newest messages from both sources.
const RECENT = 5;

export function recentMessages(siqspeak: VoiceMessage[], board: VoiceMessage[]): VoiceMessage[] {
  return [...siqspeak, ...board].sort((a, b) => b.at - a.at).slice(0, RECENT);
}

// voiceLevel is how loud a frame of microphone samples is, from 0 for silence
// to 1: the root mean square of the time-domain bytes around their midpoint
// 128, scaled so ordinary speech fills a good part of the bars.
export function voiceLevel(samples: ArrayLike<number>): number {
  if (!samples.length) return 0;
  let sum = 0;
  for (let i = 0; i < samples.length; i++) {
    const offset = (samples[i] - 128) / 128;
    sum += offset * offset;
  }
  return Math.min(1, Math.sqrt(sum / samples.length) * 4);
}
