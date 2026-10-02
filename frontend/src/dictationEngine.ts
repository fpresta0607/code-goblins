import { request } from "./api";
import { speechRecognition, type Recognizer } from "./dictation";
import { localRecognizer, type Recording, type Sound } from "./localDictation";
import { object, string } from "./types";

// Which recognizer dictation runs on in this page. The board's own is the
// default: it records in the page and has the supervisor on this machine
// recognise the sound. The browser's speech recognition, which sends the sound
// to the browser's maker, is a fallback the Overlord turns on himself.

// The speech model hears at this rate, so a recording is decoded to it.
const RATE = 16000;
const BROWSER_KEY = "cfo-dictation-browser-v1";

// record captures a microphone track with the browser's own recorder until
// it is stopped, then decodes what was recorded into samples at the model's
// rate. Nothing but the browser is needed, in a tab or in the desktop app.
export async function record(track: MediaStreamTrack): Promise<Recording> {
  const recorder = new MediaRecorder(new MediaStream([track]));
  const parts: Blob[] = [];
  recorder.ondataavailable = (event) => { if (event.data.size) parts.push(event.data); };
  recorder.start();
  return {
    stop: () => new Promise((resolve, reject) => {
      recorder.onstop = () => { decode(new Blob(parts, { type: recorder.mimeType })).then(resolve, reject); };
      recorder.stop();
    }),
    cancel: () => { recorder.ondataavailable = null; if (recorder.state !== "inactive") recorder.stop(); },
  };
}

// decode turns a recording into one channel of samples. A recording too
// short to hold any sound decodes to none.
async function decode(recording: Blob): Promise<Sound> {
  const silence: Sound = { samples: new Float32Array(0), rate: RATE };
  if (!recording.size) return silence;
  // Decoding into a context at the model's rate resamples to it.
  const context = new OfflineAudioContext(1, 1, RATE);
  try {
    const sound = await context.decodeAudioData(await recording.arrayBuffer());
    return { samples: sound.getChannelData(0), rate: sound.sampleRate };
  } catch {
    return silence;
  }
}

// recogniseWith has the supervisor recognise a WAV sound, as the board whose
// token is instance. What the supervisor refuses with is thrown as it wrote it.
export function recogniseWith(instance: () => string): (sound: Uint8Array<ArrayBuffer>) => Promise<string> {
  return async (sound) => string(object(await request("/api/dictation", undefined, { method: "POST", headers: { "Content-Type": "audio/wav", "X-CFO-Token": instance() }, body: sound })).text);
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
  return (usesBrowser() && speechRecognition()) || localRecognizer(record, recogniseWith(instance));
}

let named: Promise<string> | null = null;

// modelName asks the supervisor once which speech model it runs, and is
// empty when it could not say.
export function modelName(): Promise<string> {
  named ??= request("/api/dictation").then((status) => string(object(status).engine), () => "");
  return named;
}
