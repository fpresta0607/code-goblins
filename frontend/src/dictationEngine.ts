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

// The page's one audio context for dictation, with the capture worklet
// loaded into it. It is made as the keys are pressed, while the microphone
// opens, and kept for the next dictation: a worklet loaded only once the
// microphone was open lost a dictation's first second.
let capturing: Promise<AudioContext> | null = null;
let recordings = 0;

// The context runs from each key press and is suspended once it has gone
// this long with no recording, so a dictation that follows another soon
// finds it running. Suspended at every release, it took a busy PC half a
// second or more to resume at the next press, in which the bubble said it
// listened and nothing was recorded, so a short dictation was told as
// nothing heard.
const REST_MS = 30_000;
let resting: ReturnType<typeof setTimeout> | undefined;

function rest(context: AudioContext): void {
  clearTimeout(resting);
  resting = setTimeout(() => { if (recordings === 0) void context.suspend(); }, REST_MS);
}

function captureContext(): Promise<AudioContext> {
  if (!capturing) {
    const context = new AudioContext();
    const loaded = context.audioWorklet.addModule(CAPTURE).then(() => context);
    loaded.catch(() => { capturing = null; void context.close(); });
    capturing = loaded;
  }
  return capturing;
}

// record captures a microphone track's samples as they arrive, through the
// capture worklet, and hands on each piece of what is said at the model's
// rate as soon as it ends in a pause. It has begun once its first samples
// arrive, which a context only just made or resumed takes a while to hand
// over, though it says at once that it runs. stop asks the worklet for what
// it still holds and returns what followed the last piece. Nothing but the
// browser is needed, in a tab or in the desktop app.
export function record(track: MediaStreamTrack, piece: (sound: Sound) => void): Recording {
  let stopped = false;
  let flushed = () => {};
  let arrived = () => {};
  const first = new Promise<void>((resolve) => { arrived = resolve; });
  const capture = captureContext().then((context) => {
    recordings++;
    void context.resume();
    const pieces = new Pieces(context.sampleRate, (sound) => { if (!stopped) piece(downsample(sound, RATE)); });
    const node = new AudioWorkletNode(context, "dictation-capture", { numberOfOutputs: 0 });
    node.port.onmessage = (event: MessageEvent<Float32Array | null>) => {
      if (!event.data) { flushed(); return; }
      arrived();
      if (!stopped) pieces.push(event.data);
    };
    const source = context.createMediaStreamSource(new MediaStream([track]));
    source.connect(node);
    return { context, pieces, node, source };
  });
  capture.catch(() => undefined);
  const end = () => {
    stopped = true;
    void capture.then(({ context, node, source }) => {
      source.disconnect();
      node.port.close();
      if (--recordings === 0) rest(context);
    }, () => undefined);
  };
  return {
    began: capture.then(() => first),
    stop: async () => {
      try {
        const { pieces, node } = await capture;
        await new Promise<void>((resolve) => { flushed = resolve; node.port.postMessage("flush"); });
        return downsample(pieces.rest(), RATE);
      } finally {
        end();
      }
    },
    cancel: end,
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

// recognizerFor is the recognizer the next dictation runs on. The board's
// own loads its capture worklet and has its context running at once, while
// the microphone opens.
export function recognizerFor(instance: () => string): new () => Recognizer {
  const browser = usesBrowser() && speechRecognition();
  if (browser) return browser;
  void captureContext().then((context) => { void context.resume(); rest(context); }, () => undefined);
  return localRecognizer(record, recogniseWith(instance), warmWith(instance));
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
