import { spoken, type Recognizer } from "./dictation.ts";

// The board's own dictation: what is said while the keys are held is
// recorded in the page and handed to the supervisor on this machine, which
// runs a speech model and answers the words. Nothing leaves the PC. A long
// dictation goes in pieces, each as soon as the Overlord pauses, so its words
// are recognised while he is still speaking and only the last piece waits for
// the keys to be let go. The engine cuts a piece the model cannot take whole
// and ends each one half a second after its last loud part.

// A Sound is one channel of samples from -1 to 1 at rate samples a second.
export interface Sound { samples: Float32Array; rate: number }

// A Recording is one dictation's sound being captured: it hands on each piece
// as it is said, stop ends it and returns what followed the last piece, and
// cancel ends it and keeps nothing.
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
// scale: above a room's hum, below a voice. The engine reads it alike.
const LOUD = 0.01;
// A piece is handed on once this much quiet follows speech in it,
const PAUSE_SECONDS = 0.3;
// and it is at least this long, a phrase or more.
const PIECE_LEAST_SECONDS = 4;
// A piece that reaches this length with no such pause, as in a noisy room,
// is cut where it was quietest,
const PIECE_MOST_SECONDS = 20;
// judged over stretches this long: a pause between words, not a dip in one.
const QUIET_SPAN_SECONDS = 0.2;

function isLoud(power: number): boolean {
  return Math.sqrt(power) >= LOUD;
}

// power is the mean square of each whole 10 ms of samples at rate.
function power(samples: Float32Array, rate: number, frames: number[] = []): number[] {
  const frame = Math.max(1, Math.round(rate / 100));
  for (let at = frames.length * frame; at + frame <= samples.length; at += frame) {
    let sum = 0;
    for (let index = at; index < at + frame; index++) sum += samples[index] * samples[index];
    frames.push(sum / frame);
  }
  return frames;
}

// isSpoken says whether any 10 ms of a sound is loud.
export function isSpoken(sound: Sound): boolean {
  return power(sound.samples, sound.rate).some(isLoud);
}

// Pieces takes a recording as it arrives, at its own rate, and hands on each
// piece of it as it is said: once a piece is PIECE_LEAST_SECONDS long and
// PAUSE_SECONDS of quiet follow speech in it, or once it reaches
// PIECE_MOST_SECONDS, cut at the latest place whose QUIET_SPAN_SECONDS are
// within twice the quietest in it. rest is what followed the last piece.
export class Pieces {
  private readonly rate: number;
  private readonly frame: number;
  private readonly piece: (sound: Sound) => void;
  private samples: Float32Array;
  private length = 0;
  private frames: number[] = [];

  constructor(rate: number, piece: (sound: Sound) => void) {
    this.rate = rate;
    this.frame = Math.max(1, Math.round(rate / 100));
    this.piece = piece;
    this.samples = new Float32Array(rate * PIECE_LEAST_SECONDS);
  }

  push(chunk: Float32Array): void {
    if (this.length + chunk.length > this.samples.length) {
      const grown = new Float32Array(Math.max(this.samples.length * 2, this.length + chunk.length));
      grown.set(this.samples.subarray(0, this.length));
      this.samples = grown;
    }
    this.samples.set(chunk, this.length);
    this.length += chunk.length;
    const known = this.frames.length;
    power(this.samples.subarray(0, this.length), this.rate, this.frames);
    for (let count = known + 1; count <= this.frames.length; count++) {
      if (this.pausedAt(count)) { this.cut(count); count = 0; }
      else if (count >= PIECE_MOST_SECONDS * 100) { const at = this.quietest(); this.cut(at); count -= at; }
    }
  }

  rest(): Sound {
    return { samples: this.samples.slice(0, this.length), rate: this.rate };
  }

  // pausedAt says whether the first count frames make a piece that ends in a
  // pause after speech.
  private pausedAt(count: number): boolean {
    const pause = Math.round(PAUSE_SECONDS * 100);
    if (count < PIECE_LEAST_SECONDS * 100) return false;
    for (let index = count - pause; index < count; index++) if (isLoud(this.frames[index])) return false;
    for (let index = 0; index < count - pause; index++) if (isLoud(this.frames[index])) return true;
    return false;
  }

  private quietest(): number {
    const span = Math.round(QUIET_SPAN_SECONDS * 100);
    const quiet = (at: number) => {
      const stretch = this.frames.slice(Math.max(0, at - span / 2), at + span / 2);
      return stretch.reduce((sum, value) => sum + value, 0) / stretch.length;
    };
    const low = PIECE_LEAST_SECONDS * 100, high = PIECE_MOST_SECONDS * 100;
    let least = Infinity;
    for (let at = low; at <= high; at++) least = Math.min(least, quiet(at));
    let at = high;
    while (quiet(at) > 2 * least) at--;
    return at;
  }

  private cut(frames: number): void {
    const at = frames * this.frame;
    this.piece({ samples: this.samples.slice(0, at), rate: this.rate });
    this.samples.copyWithin(0, at, this.length);
    this.length -= at;
    this.frames = this.frames.slice(frames);
  }
}

// joined is sounds at one rate one after another.
function joined(sounds: Sound[]): Sound {
  const samples = new Float32Array(sounds.reduce((length, sound) => length + sound.samples.length, 0));
  let at = 0;
  for (const sound of sounds) { samples.set(sound.samples, at); at += sound.samples.length; }
  return { samples, rate: sounds[0]?.rate ?? 16000 };
}

// DictationSetUp is a refusal because the speech model is still being set
// up, which asking again soon does not change: the board shows how far the
// set-up is instead.
export class DictationSetUp extends Error {}

// After a piece's words could not be had, they are asked for again after
// each of these waits, with the piece kept until then.
const RETRY_MS = [500, 2000];

// localRecognizer is a recognizer the board's dictation drives as it drives
// the browser's: start records the track it is handed with open, which hands
// on each piece as it is said, and each piece's words are asked of recognise
// at once, in order. stop ends the recording before it returns, so the
// microphone can close at once, then delivers the words of every piece, once.
// What recognise refuses with, after asking again, is passed on as the
// supervisor wrote it, at once when the model is being set up; words that never came for a dictation with speech in
// it are "unheard", never "no-speech". warm is called as the recording
// begins, so the supervisor loads its engine while the words are being said.
export function localRecognizer(open: (track: MediaStreamTrack, piece: (sound: Sound) => void) => Recording, recognise: (sound: Uint8Array<ArrayBuffer>, signal: AbortSignal) => Promise<string>, warm: () => void): new () => Recognizer {
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
    // The words of each piece asked for, in order, and the turn the next
    // waits for.
    private readonly words: Promise<string>[] = [];
    private turn: Promise<unknown> = Promise.resolve();
    private isSpeech = false;
    private seconds = 0;
    // Pieces with nothing loud in them, held back: room noise, which the
    // model writes words of its own into, once anything loud is said, else
    // the whole dictation as it is, as from a quiet microphone.
    private quiet: Sound[] = [];

    private end(error?: { error: string; message?: string }): void {
      if (this.ended) return;
      this.ended = true;
      if (error) this.onerror?.(error);
      this.onend?.();
    }

    private take(piece: Sound): void {
      this.seconds += piece.samples.length / piece.rate;
      if (!isSpoken(piece)) { this.quiet.push(piece); return; }
      this.isSpeech = true;
      this.quiet = [];
      this.send(piece);
    }

    private send(sound: Sound): void {
      const words = this.turn.then(() => this.ask(wav(sound)));
      this.turn = words.catch(() => undefined);
      this.words.push(words);
    }

    private async ask(sound: Uint8Array<ArrayBuffer>): Promise<string> {
      for (let attempt = 0; ; attempt++) {
        try {
          return await recognise(sound, this.cancellation.signal);
        } catch (error) {
          if (this.cancellation.signal.aborted || error instanceof DictationSetUp || attempt >= RETRY_MS.length) throw error;
          await new Promise((resolve) => setTimeout(resolve, RETRY_MS[attempt]));
        }
      }
    }

    start(track?: MediaStreamTrack): void {
      if (!track) { this.end({ error: "audio-capture" }); return; }
      try {
        this.recording = open(track, (piece) => this.take(piece));
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
      recording.stop().then((rest) => {
        if (this.ended) return [];
        this.take(rest);
        const whole = joined(this.quiet);
        if (!this.isSpeech && whole.samples.length) this.send(whole);
        return Promise.all(this.words);
      }).then((texts) => {
        if (this.ended) return;
        const text = spoken(texts);
        if (text) {
          this.onresult?.({ resultIndex: 0, results: [[{ transcript: text }]] });
          this.end();
        } else if (this.isSpeech) this.end({ error: "unheard", message: `The engine answered no words for ${Math.round(this.seconds)} s of dictation with speech in it.` });
        else this.end({ error: "no-speech" });
      }, (error: unknown) => this.end({ error: error instanceof DictationSetUp ? "setup" : "supervisor", message: error instanceof Error ? error.message : String(error) }));
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
