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
  // onstart is told once the recognizer listens: nothing said before it is
  // heard.
  onstart: (() => void) | null;
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

// problem is a note for the Overlord, about his microphone or a dictation
// that heard nothing, and failed is a dictation whose words could not be had,
// which goes to the CFO rather than onto the board.
export interface DictationEvents { heard: (text: string) => void; listening: (on: boolean) => void; problem: (note: string) => void; failed: (reason: string) => void }

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

// inDesktopApp says whether the page runs in the desktop app, whose WebView2
// puts its own object in the page as chrome.webview, which no browser tab has.
// Under the unit tests there is no window at all.
export function inDesktopApp(): boolean {
  return typeof window !== "undefined" && !!(window as unknown as { chrome?: { webview?: unknown } }).chrome?.webview;
}

// microphoneProblem explains a microphone that could not be opened, by the
// error's name. The desktop app's WebView2 grants the page every permission
// itself, so a microphone refused there was refused by Windows, which is
// where it is allowed again.
export function microphoneProblem(name: string, app: boolean): string {
  if (name === "NotAllowedError" || name === "SecurityError") {
    return app ? "The microphone is blocked for Code Goblins by Windows. Turn on Microphone access and Let desktop apps access your microphone in Windows Settings > Privacy & security > Microphone, then hold Ctrl+Shift+Space again." : dictationProblem("not-allowed");
  }
  if (name === "NotFoundError" || name === "NotReadableError") return dictationProblem("audio-capture");
  return "Dictation stopped: the microphone could not be opened.";
}

function captureProblem(error: unknown): string {
  return microphoneProblem(error instanceof Error ? error.name : "", inDesktopApp());
}

const RECOGNITION_MS = 120_000;

// A recognizer handed the microphone listens within moments. One that does
// not by then cannot record, and the CFO is told.
const LISTEN_MS = 5000;

// A browser's recognizer ends by itself after a stretch of silence, or once it
// has listened a good while, and one that listened at least this long is
// started again while the keys are held. One that ends sooner cannot listen,
// and starting it again would only spin.
const SEGMENT_LEAST_MS = 1000;

// A Hold is one press of the keys until their release: the recognizer hearing
// it now, the phrases heard in it so far, and its words once it is over.
interface Hold {
  recognizer: Recognizer;
  phrases: string[];
  // null means recognition is unfinished.
  text: string | null;
  isListening: boolean;
  // When its recognizer was started, in ms.
  since: number;
  timer?: ReturnType<typeof setTimeout>;
}

// deafen stops a recognizer's events from reaching anyone.
function deafen(recognizer: Recognizer): void {
  recognizer.onstart = null;
  recognizer.onresult = null;
  recognizer.onerror = null;
  recognizer.onend = null;
}

export class Dictation {
  private readonly events: DictationEvents;
  private readonly recognition: () => RecognizerClass;
  private readonly lang: string;
  private readonly microphone: (() => Promise<Capture>) | null;
  // The hold whose keys are down, and the one pressed last.
  private hold: Hold | null = null;
  private latest: Hold | null = null;
  private capture: Capture | null = null;
  // The holds whose words are not typed yet, in capture order.
  private readonly pending = new Set<Hold>();

  // With a microphone, the recognizer listens to the track that microphone
  // opens, so the waveform and the words come from one capture.
  constructor(events: DictationEvents, recognition: () => RecognizerClass, lang: string, microphone: (() => Promise<Capture>) | null = null) {
    this.events = events;
    this.recognition = recognition;
    this.lang = lang;
    this.microphone = microphone;
  }

  start(): void {
    if (this.hold) return;
    const hold: Hold = { recognizer: new (this.recognition())(), phrases: [], text: null, isListening: false, since: 0 };
    this.attend(hold);
    this.pending.add(hold);
    this.hold = this.latest = hold;
    this.events.problem("");
    if (!this.microphone) { this.begin(hold); return; }
    this.microphone().then((capture) => {
      // Released or closed while the microphone opened: keep nothing open.
      if (this.hold !== hold) { capture.close(); return; }
      this.capture = capture;
      this.begin(hold);
    }, (error: unknown) => {
      if (this.hold !== hold) return;
      this.events.problem(captureProblem(error));
      this.finish(hold, "");
    });
  }

  // attend sets hold's recognizer up and hears its events. The bubble says it
  // listens only once the recognizer does, so what it shows is being heard.
  private attend(hold: Hold): void {
    const recognizer = hold.recognizer;
    recognizer.continuous = true;
    recognizer.interimResults = false;
    recognizer.lang = this.lang;
    recognizer.onstart = () => {
      // A hold already listening goes on under a recognizer started again.
      if (hold.isListening) return;
      hold.isListening = true;
      clearTimeout(hold.timer);
      this.events.listening(true);
    };
    // Without interim results every result the browser sends is final.
    recognizer.onresult = (event) => {
      for (let i = event.resultIndex; i < event.results.length; i++) hold.phrases.push(event.results[i][0].transcript);
    };
    recognizer.onerror = (event) => {
      // Silence is no failure. The recognizer's end follows it, and says what
      // the hold came to.
      if (event.error === "no-speech") return;
      if (event.error === "supervisor" || event.error === "unheard") this.events.failed(event.message || event.error);
      else {
        const note = event.message || dictationProblem(event.error);
        if (note) this.events.problem(note);
      }
      this.finish(hold, "");
    };
    recognizer.onend = () => {
      // Ended while the keys are held: the hold goes on, on a recognizer of
      // its own.
      if (this.hold === hold && hold.isListening && Date.now() - hold.since >= SEGMENT_LEAST_MS) {
        deafen(recognizer);
        hold.recognizer = new (this.recognition())();
        this.attend(hold);
        this.begin(hold);
        return;
      }
      const text = spoken(hold.phrases);
      // A hold that listened and heard no words says so once, unless he is
      // dictating again already, when the note would read as being about
      // the hold he is in.
      if (!text && hold.isListening && this.latest === hold) this.events.problem(dictationProblem("no-speech"));
      this.finish(hold, text);
    };
  }

  private finish(hold: Hold, text: string): void {
    if (!this.pending.has(hold) || hold.text !== null) return;
    hold.text = text;
    clearTimeout(hold.timer);
    deafen(hold.recognizer);
    if (this.hold === hold) {
      this.hold = null;
      this.release();
      this.events.listening(false);
    }
    for (const prior of this.pending) {
      if (prior.text === null) break;
      this.pending.delete(prior);
      if (prior.text) this.events.heard(prior.text);
    }
  }

  // begin starts hold's recognizer, on the microphone's track when one is
  // open.
  private begin(hold: Hold): void {
    hold.since = Date.now();
    if (!hold.isListening) {
      hold.timer = setTimeout(() => {
        this.events.failed("Dictation did not begin recording within 5 seconds of the microphone opening, so nothing was typed.");
        this.finish(hold, "");
        hold.recognizer.abort();
      }, LISTEN_MS);
    }
    const recognizer = hold.recognizer, track = this.capture?.track;
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
  // in capture order, so the next dictation can start meanwhile.
  stop(): void {
    const hold = this.hold;
    if (!hold) return;
    this.hold = null;
    this.events.listening(false);
    // Released before anything listened, as before the microphone opened:
    // nothing was said to it, so nothing is asked or told of it.
    if (!hold.isListening) {
      this.release();
      this.finish(hold, "");
      hold.recognizer.abort();
      return;
    }
    hold.recognizer.stop();
    this.release();
    if (hold.text === null) {
      hold.timer = setTimeout(() => {
        if (hold.text !== null) return;
        this.events.failed("Dictation did not finish within 120 seconds of the keys being let go, so its words were not typed.");
        this.finish(hold, "");
        hold.recognizer.abort();
      }, RECOGNITION_MS);
    }
  }

  // dispose stops listening without typing anything, for a terminal going
  // away, and drops the words still on their way.
  dispose(): void {
    this.hold = this.latest = null;
    this.release();
    for (const hold of this.pending) {
      clearTimeout(hold.timer);
      deafen(hold.recognizer);
      if (hold.text === null) hold.recognizer.abort();
    }
    this.pending.clear();
  }
}
