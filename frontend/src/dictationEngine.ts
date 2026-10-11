import { request } from "./api";
import { speechRecognition, type Capture, type Recognizer } from "./dictation";
import { DictationSetUp, downsample, localRecognizer, Pieces, type Recording, type Sound } from "./localDictation";
import { object, string } from "./types";
import { voiceLevel } from "./voice";

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

// The page's one recorder for dictation: an audio context with the capture
// worklet loaded into it, the worklet's node, and the analyser the bubble's
// bars read. It is made once, while the page is idle after a terminal or a
// message box first shows, or else at the first press, and kept: a worklet
// loaded only once the microphone was open lost a dictation's first second,
// and a context made at the press held the page still for 0.1 to 0.2 s on
// this PC, while the bubble waited to show that it listened.
interface Recorder { context: AudioContext; node: AudioWorkletNode; analyser: AnalyserNode }
let recorder: Promise<Recorder> | null = null;
// What the worklet hands over goes to the recordings in the order they
// began, each until the null that ends it.
const takers: ((samples: Float32Array | null) => void)[] = [];
let recordings = 0;

function recorderFor(): Promise<Recorder> {
  if (!recorder) {
    const context = new AudioContext();
    const made = context.audioWorklet.addModule(CAPTURE).then(() => {
      const node = new AudioWorkletNode(context, "dictation-capture", { numberOfOutputs: 0 });
      node.port.onmessage = (event: MessageEvent<Float32Array | null>) => {
        takers[0]?.(event.data);
        if (!event.data) takers.shift();
      };
      const analyser = context.createAnalyser();
      analyser.fftSize = 512;
      return { context, node, analyser };
    });
    made.catch(() => { recorder = null; void context.close(); });
    recorder = made;
  }
  return recorder;
}

// prepareDictation makes the recorder while the page is idle, before a press
// needs it, and leaves it suspended until then.
export function prepareDictation(): void {
  if (recorder) return;
  requestIdleCallback(() => {
    void recorderFor().then(rest, () => undefined);
  }, { timeout: 5000 });
}

// The microphone opens at the first press and stays open while the board has
// the focus, so every later press records from the moment the keys go down,
// which the worklet's last second still holds. It closes when the window
// loses the focus or the page is hidden, and once IDLE_MS pass with no
// dictation; the next press opens it again, and what is said while it opens
// is not heard. Held open before any press, it would show a microphone in use
// whenever the board had the focus (the CFO's decision 1000850, 2026-10-11).
const IDLE_MS = 30 * 60_000;
interface Microphone { stream: MediaStream; source: MediaStreamAudioSourceNode | null; analyser: AnalyserNode | null }
let microphone: Promise<Microphone> | null = null;
let idle: ReturnType<typeof setTimeout> | undefined;
let watching = false;

// rest suspends the recorder while no microphone is open and nothing records.
function rest(): void {
  if (recordings === 0 && !microphone) void recorder?.then(({ context }) => context.suspend(), () => undefined);
}

function shut({ stream, source }: Microphone): void {
  source?.disconnect();
  for (const track of stream.getTracks()) track.stop();
  rest();
}

function closeMicrophone(): void {
  clearTimeout(idle);
  const closing = microphone;
  microphone = null;
  void closing?.then(shut, () => undefined);
}

function watchFocus(): void {
  if (watching) return;
  watching = true;
  window.addEventListener("blur", closeMicrophone);
  document.addEventListener("visibilitychange", () => { if (document.hidden) closeMicrophone(); });
}

// openMicrophone is a hold's use of the page's microphone, opening it at the
// first press. The recorder is made alongside, after the microphone is asked
// for, and woken from its rest once the microphone is open, whose source holds
// what it hears meanwhile. Woken at the press too, it slowed the microphone
// itself: after the window took the focus again the first sound kept came a
// median 49 ms after the press in the desktop window and 239 ms in Chromium,
// against 5 ms and 155 ms this way (2026-10-11). Closing a hold's capture
// leaves the microphone open for the next press, until IDLE_MS pass. A page
// with no microphone to ask for, as one reached over plain HTTP, refuses as a
// blocked one does, never in the middle of the press.
export async function openMicrophone(): Promise<Capture> {
  clearTimeout(idle);
  if (!microphone) {
    watchFocus();
    const asked = navigator.mediaDevices.getUserMedia({ audio: true });
    // A recorder that cannot be made fails the recording, which tells the CFO.
    const made = recorderFor().catch(() => null);
    const opening = asked.then(async (stream) => {
      const parts = await made;
      if (!parts) return { stream, source: null, analyser: null };
      const source = parts.context.createMediaStreamSource(stream);
      source.connect(parts.node);
      source.connect(parts.analyser);
      void parts.context.resume();
      return { stream, source, analyser: parts.analyser };
    });
    microphone = opening;
    // One closed while it opened is let go at once, and one refused is asked
    // for again at the next press.
    opening.then((opened) => { if (microphone !== opening) shut(opened); }, () => {
      if (microphone !== opening) return;
      microphone = null;
      rest();
    });
  }
  return microphone.then(({ stream, analyser }) => {
    const samples = new Uint8Array(analyser?.fftSize ?? 0);
    return {
      track: stream.getAudioTracks()[0],
      level: () => {
        if (!analyser) return 0;
        analyser.getByteTimeDomainData(samples);
        return voiceLevel(samples);
      },
      close: () => {
        clearTimeout(idle);
        idle = setTimeout(closeMicrophone, IDLE_MS);
      },
    };
  });
}

// record hands on what the page's microphone hears from since, the moment the
// keys went down, through the capture worklet: what the worklet still keeps
// from the press, then the samples as they arrive, each piece of what is said
// at the model's rate as soon as it ends in a pause. It has begun once its
// first samples arrive, which a microphone only just opened takes a while to
// hand over. stop asks the worklet for what it still holds and returns what
// followed the last piece. Nothing but the browser is needed, in a tab or in
// the desktop app.
export function record(piece: (sound: Sound) => void, since: number): Recording {
  let stopped = false;
  let flushed = () => {};
  let arrived = () => {};
  const first = new Promise<void>((resolve) => { arrived = resolve; });
  const capture = recorderFor().then(({ context, node }) => {
    void context.resume();
    const pieces = new Pieces(context.sampleRate, (sound) => { if (!stopped) piece(downsample(sound, RATE)); });
    takers.push((samples) => {
      if (!samples) { flushed(); return; }
      arrived();
      if (!stopped) pieces.push(samples);
    });
    // The press, as a frame of the context's clock.
    node.port.postMessage({ from: Math.round((context.currentTime - (performance.now() - since) / 1000) * context.sampleRate) });
    return { node, pieces };
  });
  capture.catch(() => undefined);
  recordings++;
  const end = () => {
    stopped = true;
    recordings--;
    rest();
  };
  return {
    began: capture.then(() => first),
    stop: async () => {
      try {
        const { node, pieces } = await capture;
        await new Promise<void>((resolve) => { flushed = resolve; node.port.postMessage("flush"); });
        return downsample(pieces.rest(), RATE);
      } finally {
        end();
      }
    },
    cancel: () => {
      end();
      void capture.then(({ node }) => node.port.postMessage("flush"), () => undefined);
    },
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
  const browser = usesBrowser() && speechRecognition();
  if (browser) return browser;
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
