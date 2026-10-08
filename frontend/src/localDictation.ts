import type { Recognizer } from "./dictation.ts";

// The board's own dictation: what is said while the keys are held is
// recorded in the page and handed to the supervisor on this machine, which
// runs a speech model and answers the words. Nothing leaves the PC.

// A Sound is one channel of samples from -1 to 1 at rate samples a second.
export interface Sound { samples: Float32Array; rate: number }

// A Recording is one dictation's sound being captured: stop ends it and
// returns what was recorded, cancel ends it and keeps nothing.
export interface Recording { stop: () => Promise<Sound>; cancel: () => void }

// wav writes a sound as a 16-bit mono WAV file at its own rate.
export function wav({ samples, rate }: Sound): Uint8Array<ArrayBuffer> {
  const bytes = new Uint8Array(44 + samples.length * 2);
  const view = new DataView(bytes.buffer);
  const tag = (at: number, text: string) => { for (let i = 0; i < text.length; i++) bytes[at + i] = text.charCodeAt(i); };
  tag(0, "RIFF");
  view.setUint32(4, bytes.length - 8, true);
  tag(8, "WAVE");
  tag(12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, rate, true);
  view.setUint32(28, rate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  tag(36, "data");
  view.setUint32(40, samples.length * 2, true);
  for (let i = 0; i < samples.length; i++) {
    const sample = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(44 + i * 2, Math.round(sample < 0 ? sample * 32768 : sample * 32767), true);
  }
  return bytes;
}

// downsample brings a sound down to rate by averaging each run of samples
// that makes one sample at rate, a light low-pass that speech needs no more
// than; a part-run left at the end is dropped. A sound at or under rate is
// kept as it is, and the engine takes its rate from the WAV.
export function downsample(sound: Sound, rate: number): Sound {
  if (sound.rate <= rate) return sound;
  const step = sound.rate / rate;
  const samples = new Float32Array(Math.floor(sound.samples.length / step));
  for (let index = 0; index < samples.length; index++) {
    const from = Math.round(index * step);
    const to = Math.round((index + 1) * step);
    let sum = 0;
    for (let at = from; at < to; at++) sum += sound.samples[at];
    samples[index] = sum / (to - from);
  }
  return { samples, rate };
}

// A part of a sound is loud when its 10 ms reach this RMS, -40 dB of full
// scale: above a room's hum, below a voice.
const LOUD = 0.01;

// endInQuiet ends a sound half a second after its last loud part: a longer
// quiet end is cut, and a shorter one, as when the keys are let go mid-word,
// is made up with silence. A model that writes until it hears the end, as
// Moonshine does, then ends the line there rather than running on, repeating
// itself, and a long quiet end does not make it answer nothing. A sound with
// nothing loud in it is kept as it is.
export function endInQuiet(sound: Sound): Sound {
  const step = Math.max(1, Math.round(sound.rate / 100));
  let end = 0;
  for (let at = 0; at < sound.samples.length; at += step) {
    const part = sound.samples.subarray(at, at + step);
    let sum = 0;
    for (const sample of part) sum += sample * sample;
    if (Math.sqrt(sum / part.length) >= LOUD) end = at + part.length;
  }
  if (!end) return sound;
  const keep = end + Math.round(sound.rate / 2);
  if (sound.samples.length >= keep) return { samples: sound.samples.subarray(0, keep), rate: sound.rate };
  const samples = new Float32Array(keep);
  samples.set(sound.samples);
  return { samples, rate: sound.rate };
}

// localRecognizer is a recognizer the board's dictation drives as it drives
// the browser's: start records the track it is handed with open, and stop
// ends the recording before it returns, so the microphone can close at once,
// then hands it to recognise and delivers the words it answers, once.
// What recognise refuses with is passed on as the supervisor wrote it. warm
// is called as the recording begins, so the supervisor loads its engine while
// the words are still being said.
export function localRecognizer(open: (track: MediaStreamTrack) => Recording, recognise: (sound: Uint8Array<ArrayBuffer>, signal: AbortSignal) => Promise<string>, warm: () => void): new () => Recognizer {
  return class implements Recognizer {
    continuous = false;
    interimResults = false;
    lang = "";
    onresult: Recognizer["onresult"] = null;
    onerror: Recognizer["onerror"] = null;
    onend: Recognizer["onend"] = null;
    private recording: Recording | null = null;
    private ended = false;
    private readonly cancellation = new AbortController();

    private end(error?: { error: string; message?: string }): void {
      if (this.ended) return;
      this.ended = true;
      if (error) this.onerror?.(error);
      this.onend?.();
    }

    start(track?: MediaStreamTrack): void {
      if (!track) { this.end({ error: "audio-capture" }); return; }
      try {
        this.recording = open(track);
      } catch {
        this.end({ error: "audio-capture" });
        return;
      }
      warm();
    }

    stop(): void {
      const recording = this.recording;
      this.recording = null;
      if (!recording) return;
      recording.stop().then((sound) => {
        if (this.ended) return "";
        return sound.samples.length ? recognise(wav(sound), this.cancellation.signal) : "";
      }).then((text) => {
        if (this.ended) return;
        if (!text) { this.end({ error: "no-speech" }); return; }
        this.onresult?.({ resultIndex: 0, results: [[{ transcript: text }]] });
        this.end();
      }, (error: unknown) => this.end({ error: "supervisor", message: error instanceof Error ? error.message : String(error) }));
    }

    abort(): void {
      this.cancellation.abort();
      const recording = this.recording;
      this.recording = null;
      recording?.cancel();
      this.end({ error: "aborted" });
    }
  };
}
