import { request } from "./api";
import { speechRecognition, type Recognizer } from "./dictation";
import { DictationSetUp, downsample, localRecognizer, Pieces, type Recording, type Sound } from "./localDictation";
import { object, string } from "./types";

// Which recognizer dictation runs on in this page. The board's own is the
// default: it records in the page and has the supervisor on this machine
// recognise the sound. The browser's speech recognition, which sends the sound
// to the browser's maker, is a fallback the Overlord turns on himself.

// The speech model hears at this rate, so a recording is brought down to it.
const RATE = 16000;
// The worklet the page records through, which the build copies from
// public/assets.
const CAPTURE = "/assets/dictation-capture.js";
const BROWSER_KEY = "cfo-dictation-browser-v1";

// record captures a microphone track's samples as they arrive, through the
// capture worklet, and hands on each piece of what is said at the model's
// rate as soon as it ends in a pause. stop asks the worklet for what it still
// holds and returns what followed the last piece. Nothing but the browser is
// needed, in a tab or in the desktop app.
export function record(track: MediaStreamTrack, piece: (sound: Sound) => void): Recording {
  const context = new AudioContext();
  void context.resume();
  let stopped = false;
  const pieces = new Pieces(context.sampleRate, (sound) => { if (!stopped) piece(downsample(sound, RATE)); });
  let flushed = () => {};
  const capture = context.audioWorklet.addModule(CAPTURE).then(() => {
    const node = new AudioWorkletNode(context, "dictation-capture", { numberOfOutputs: 0 });
    node.port.onmessage = (event: MessageEvent<Float32Array | null>) => {
      if (event.data) { if (!stopped) pieces.push(event.data); } else flushed();
    };
    context.createMediaStreamSource(new MediaStream([track])).connect(node);
    return node;
  });
  capture.catch(() => undefined);
  return {
    stop: async () => {
      try {
        const node = await capture;
        await new Promise<void>((resolve) => { flushed = resolve; node.port.postMessage("flush"); });
        return downsample(pieces.rest(), RATE);
      } finally {
        stopped = true;
        void context.close();
      }
    },
    cancel: () => { stopped = true; void context.close(); },
  };
}

// recogniseWith has the supervisor recognise a WAV sound, as the board whose
// token is instance. What the supervisor refuses with is thrown as it wrote
// it, as a DictationSetUp while its speech model is being set up.
export function recogniseWith(instance: () => string): (sound: Uint8Array<ArrayBuffer>, signal: AbortSignal) => Promise<string> {
  return async (sound, signal) => {
    try {
      return string(object(await request("/api/dictation", signal, { method: "POST", headers: { "Content-Type": "audio/wav", "X-CFO-Token": instance() }, body: sound })).text);
    } catch (error) {
      const status = signal.aborted ? null : await dictationStatus().catch(() => null);
      if (status && status.state !== "ready") throw new DictationSetUp(error instanceof Error ? error.message : String(error));
      throw error;
    }
  };
}

// warmWith has the supervisor load its engine as a dictation begins, as the
// board whose token is instance. Nothing waits on it: a dictation that could
// not run says why when it is sent. Its empty answer is read to the end, so
// the request ends there.
export function warmWith(instance: () => string): () => void {
  return () => { fetch("/api/dictation/warm", { method: "POST", headers: { "X-CFO-Token": instance() } }).then((response) => response.text()).catch(() => {}); };
}

function store(): Storage | null {
  try { return localStorage; } catch { return null; }
}

// browserOffered says whether this browser has a speech recognition to fall
// back on; the desktop app has none.
export function browserOffered(): boolean {
  return !!speechRecognition();
}

// usesBrowser says whether the Overlord turned the browser's speech
// recognition on in this browser, where it exists.
export function usesBrowser(): boolean {
  return browserOffered() && store()?.getItem(BROWSER_KEY) === "on";
}

export function setUsesBrowser(on: boolean): void {
  try {
    if (on) store()?.setItem(BROWSER_KEY, "on");
    else store()?.removeItem(BROWSER_KEY);
  } catch { /* the choice lasts until the page is closed */ }
}

// recognizerFor is the recognizer the next dictation runs on.
export function recognizerFor(instance: () => string): new () => Recognizer {
  return (usesBrowser() && speechRecognition()) || localRecognizer(record, recogniseWith(instance), warmWith(instance));
}

// DictationStatus is what the supervisor says of its speech model: ready,
// fetching with how far the download is, or missing with why it failed.
export interface DictationStatus { state: string; note: string }

export async function dictationStatus(): Promise<DictationStatus> {
  const status = object(await request("/api/dictation"));
  return { state: string(status.state), note: string(status.note) };
}

let named: Promise<string> | null = null;

// modelName asks the supervisor once which speech model it runs, and is
// empty when it could not say.
export function modelName(): Promise<string> {
  named ??= request("/api/dictation").then((status) => string(object(status).engine), () => "");
  return named;
}
