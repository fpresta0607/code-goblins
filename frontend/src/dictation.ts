import { voiceLevel } from "./voice.ts";

// Push-to-talk dictation into a terminal: holding Ctrl+Shift+Space listens
// through a recognizer, and releasing it types what was heard into the
// terminal that has the keyboard, as one line the Overlord sends with Enter.
// The recognizer is the board's own, which hands the sound to the supervisor
// on this machine (localDictation.ts), or the browser's when he turns that on.

export interface Recognizer {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  onresult: ((event: { resultIndex: number; results: ArrayLike<ArrayLike<{ transcript: string }>> }) => void) | null;
  // message, when a recognizer gives one, is the note to show as it is.
  onerror: ((event: { error: string; message?: string }) => void) | null;
  onend: (() => void) | null;
  // start listens to the track given, or opens its own microphone without one.
  start(track?: MediaStreamTrack): void;
  stop(): void;
  abort(): void;
}

type RecognizerClass = new () => Recognizer;

export function speechRecognition(): RecognizerClass | null {
  const scope = window as unknown as { SpeechRecognition?: RecognizerClass; webkitSpeechRecognition?: RecognizerClass };
  return scope.SpeechRecognition || scope.webkitSpeechRecognition || null;
}

// dictationKey says what a key event means for dictation: whether it starts
// or stops listening, and whether the terminal must not see it. Releasing
// Ctrl or Shift first also stops, and that release still reaches the terminal.
export function dictationKey(event: { type: string; code: string; key: string; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean; repeat: boolean }): { action: "start" | "stop" | null; swallow: boolean } | null {
  if (event.code === "Space" && event.ctrlKey && event.shiftKey && !event.altKey && !event.metaKey) {
    if (event.type === "keydown") return { action: event.repeat ? null : "start", swallow: true };
    if (event.type === "keyup") return { action: "stop", swallow: true };
    return { action: null, swallow: true };
  }
  if (event.type === "keyup" && (event.code === "Space" || event.key === "Control" || event.key === "Shift")) return { action: "stop", swallow: false };
  return null;
}

// spoken joins the phrases heard into one line of words; a line break would
// press Enter in the terminal.
export function spoken(phrases: string[]): string {
  return phrases.join(" ").replace(/\s+/g, " ").trim();
}

export function dictationProblem(error: string): string {
  switch (error) {
    case "aborted": return "";
    case "not-allowed":
    case "service-not-allowed": return "The microphone is blocked for the board. Allow it in the browser's site settings, then hold Ctrl+Shift+Space again.";
    case "audio-capture": return "No microphone was found.";
    case "network": return "Speech recognition in this browser needs a network connection.";
    case "no-speech": return "Nothing was heard.";
    default: return "Dictation stopped: " + error + ".";
  }
}

export interface DictationEvents { heard: (text: string) => void; listening: (on: boolean) => void; problem: (note: string) => void }

// A Capture is one open microphone: its track, which the recognizer listens
// to, and how loud it is right now, which the voice bubble's waveform shows.
// Nothing else reads, keeps or sends its audio.
export interface Capture { track: MediaStreamTrack; level: () => number; close: () => void }

export async function openMicrophone(): Promise<Capture> {
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
  const context = new AudioContext();
  const analyser = context.createAnalyser();
  analyser.fftSize = 512;
  context.createMediaStreamSource(stream).connect(analyser);
  void context.resume();
  const samples = new Uint8Array(analyser.fftSize);
  return {
    track: stream.getAudioTracks()[0],
    level: () => { analyser.getByteTimeDomainData(samples); return voiceLevel(samples); },
    close: () => { for (const track of stream.getTracks()) track.stop(); void context.close(); },
  };
}

function captureProblem(error: unknown): string {
  const name = error instanceof Error ? error.name : "";
  if (name === "NotAllowedError" || name === "SecurityError") return dictationProblem("not-allowed");
  if (name === "NotFoundError" || name === "NotReadableError") return dictationProblem("audio-capture");
  return "Dictation stopped: the microphone could not be opened.";
}

export class Dictation {
  private readonly events: DictationEvents;
  private readonly recognition: () => RecognizerClass;
  private readonly lang: string;
  private readonly microphone: (() => Promise<Capture>) | null;
  private recognizer: Recognizer | null = null;
  private started = false;
  private capture: Capture | null = null;
  // Recognizers that stopped listening and have not said their words yet.
  private readonly pending = new Set<Recognizer>();

  // With a microphone, the recognizer listens to the track that microphone
  // opens, so the waveform and the words come from one capture.
  constructor(events: DictationEvents, recognition: () => RecognizerClass, lang: string, microphone: (() => Promise<Capture>) | null = null) {
    this.events = events;
    this.recognition = recognition;
    this.lang = lang;
    this.microphone = microphone;
  }

  start(): void {
    if (this.recognizer) return;
    const recognizer = new (this.recognition())();
    recognizer.continuous = true;
    recognizer.interimResults = false;
    recognizer.lang = this.lang;
    const phrases: string[] = [];
    // Without interim results every result the browser sends is final.
    recognizer.onresult = (event) => {
      for (let i = event.resultIndex; i < event.results.length; i++) phrases.push(event.results[i][0].transcript);
    };
    recognizer.onerror = (event) => { const note = event.message || dictationProblem(event.error); if (note) this.events.problem(note); };
    recognizer.onend = () => {
      this.pending.delete(recognizer);
      // A recognizer that ends by itself, as on an error, ends the listening.
      if (this.recognizer === recognizer) {
        this.recognizer = null;
        this.release();
        this.events.listening(false);
      }
      const text = spoken(phrases);
      if (text) this.events.heard(text);
    };
    this.recognizer = recognizer;
    this.started = false;
    this.events.problem("");
    if (!this.microphone) { this.begin(recognizer); return; }
    this.microphone().then((capture) => {
      // Released or closed while the microphone opened: keep nothing open.
      if (this.recognizer !== recognizer) { capture.close(); return; }
      this.capture = capture;
      this.begin(recognizer, capture.track);
    }, (error: unknown) => {
      if (this.recognizer !== recognizer) return;
      this.recognizer = null;
      this.events.listening(false);
      this.events.problem(captureProblem(error));
    });
  }

  private begin(recognizer: Recognizer, track?: MediaStreamTrack): void {
    this.started = true;
    if (!track) recognizer.start();
    else {
      try {
        recognizer.start(track);
      } catch (error) {
        // A recognizer that cannot take a track opens its own microphone, so
        // this one closes rather than capture twice, and shows no level.
        if (!(error instanceof TypeError)) throw error;
        this.release();
        recognizer.start();
      }
    }
    if (this.recognizer === recognizer) this.events.listening(true);
  }

  private release(): void {
    this.capture?.close();
    this.capture = null;
  }

  // level is how loud the microphone is while listening, from 0 to 1.
  level(): number {
    return this.capture?.level() ?? 0;
  }

  // stop ends the listening at once: the bubble goes idle and the microphone
  // closes. The recognizer has what was said by then and its words are typed
  // when it has them, so the next dictation can start meanwhile.
  stop(): void {
    const recognizer = this.recognizer;
    if (!recognizer) return;
    this.recognizer = null;
    this.events.listening(false);
    // Released before the microphone opened: nothing was heard.
    if (!this.started) return;
    this.pending.add(recognizer);
    recognizer.stop();
    this.release();
  }

  // dispose stops listening without typing anything, for a terminal going
  // away, and drops the words still on their way.
  dispose(): void {
    const listening = this.recognizer && this.started ? [this.recognizer] : [];
    this.recognizer = null;
    this.release();
    for (const recognizer of [...this.pending, ...listening]) {
      recognizer.onresult = null;
      recognizer.onerror = null;
      recognizer.onend = null;
      recognizer.abort();
    }
    this.pending.clear();
  }
}
