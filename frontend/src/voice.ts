import { array, object, string } from "./types.ts";

// What a terminal's voice bubble lists: the board's own push-to-talk
// dictations, from every box in every window, which the supervisor keeps on
// this PC as one list.

export interface VoiceMessage { text: string; at: number }

// "dismissed" once the bubble's first-visit hint has been put away, or
// spent by the quick tour, which says the same.
export const VOICE_HINT_KEY = "cfo-voice-hint-v1";

// parseDictations reads the supervisor's list. A broken entry reads as none,
// leaving the rest be.
export function parseDictations(value: unknown): VoiceMessage[] {
  return array(object(value).messages).flatMap((raw) => {
    try {
      const entry = object(raw), text = string(entry.text);
      return text && typeof entry.at === "number" && Number.isFinite(entry.at) ? [{ text, at: entry.at }] : [];
    } catch { return []; }
  });
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
