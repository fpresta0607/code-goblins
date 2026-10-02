import { array, object, string } from "./types.ts";

// What a terminal's voice bubble lists: the board's own push-to-talk
// dictations for that pane, which stay in this browser only.

export interface VoiceMessage { text: string; at: number }

const time = (value: unknown) => typeof value === "number" && Number.isFinite(value) ? value : null;

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
        return text && at !== null ? [{ text, at }] : [];
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
  const messages = [{ text, at }, ...(kept[pane] || [])].slice(0, BOARD_DICTATIONS);
  const panes = Object.entries({ ...kept, [pane]: messages })
    .sort(([, a], [, b]) => (b[0]?.at ?? 0) - (a[0]?.at ?? 0)).slice(0, BOARD_PANES);
  try {
    storage?.setItem(DICTATIONS_KEY, JSON.stringify(Object.fromEntries(panes)));
  } catch { /* shown for this visit only */ }
  return messages;
}

// hostPane is the pane a native host's query names: its goblin's task, the
// same key a Herdr pane uses, so a relaunched goblin keeps its dictations, or
// the CFO.
export function hostPane(query: string): string {
  return new URLSearchParams(query).get("task") || "cfo";
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
