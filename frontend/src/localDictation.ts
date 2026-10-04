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

// localRecognizer is a recognizer the board's dictation drives as it drives
// the browser's: start records the track it is handed with open, and stop
// ends the recording before it returns, so the microphone can close at once,
// then hands it to recognise and delivers the words it answers, once.
// What recognise refuses with is passed on as the supervisor wrote it.
export function localRecognizer(open: (track: MediaStreamTrack) => Recording, recognise: (sound: Uint8Array<ArrayBuffer>) => Promise<string>): new () => Recognizer {
  return class implements Recognizer {
    continuous = false;
    interimResults = false;
    lang = "";
    onresult: Recognizer["onresult"] = null;
    onerror: Recognizer["onerror"] = null;
    onend: Recognizer["onend"] = null;
    private recording: Recording | null = null;
    private ended = false;

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
      }
    }

    stop(): void {
      const recording = this.recording;
      this.recording = null;
      if (!recording) return;
      recording.stop().then((sound) => sound.samples.length ? recognise(wav(sound)) : "").then((text) => {
        if (this.ended) return;
        if (!text) { this.end({ error: "no-speech" }); return; }
        this.onresult?.({ resultIndex: 0, results: [[{ transcript: text }]] });
        this.end();
      }, (error: unknown) => this.end({ error: "supervisor", message: error instanceof Error ? error.message : String(error) }));
    }

    abort(): void {
      const recording = this.recording;
      this.recording = null;
      recording?.cancel();
      this.end({ error: "aborted" });
    }
  };
}
