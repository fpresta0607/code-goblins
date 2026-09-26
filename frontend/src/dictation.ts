// Push-to-talk dictation into a terminal: holding Ctrl+Shift+Space listens
// through the browser's own speech recognition, and releasing it types what
// was heard into the terminal that has the keyboard, as one line the Overlord
// sends with Enter. Nothing is installed; a browser without speech recognition
// says so.

export interface Recognizer {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  onresult: ((event: { resultIndex: number; results: ArrayLike<ArrayLike<{ transcript: string }>> }) => void) | null;
  onerror: ((event: { error: string }) => void) | null;
  onend: (() => void) | null;
  start(): void;
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

const UNSUPPORTED = "This browser has no speech recognition, so dictation is unavailable. Edge and Chrome have it.";

export class Dictation {
  private readonly events: DictationEvents;
  private readonly recognition: () => RecognizerClass | null;
  private readonly lang: string;
  private recognizer: Recognizer | null = null;
  private phrases: string[] = [];

  constructor(events: DictationEvents, recognition: () => RecognizerClass | null, lang: string) {
    this.events = events;
    this.recognition = recognition;
    this.lang = lang;
  }

  start(): void {
    if (this.recognizer) return;
    const Recognition = this.recognition();
    if (!Recognition) { this.events.problem(UNSUPPORTED); return; }
    const recognizer = new Recognition();
    recognizer.continuous = true;
    recognizer.interimResults = false;
    recognizer.lang = this.lang;
    this.phrases = [];
    // Without interim results every result the browser sends is final.
    recognizer.onresult = (event) => {
      for (let i = event.resultIndex; i < event.results.length; i++) this.phrases.push(event.results[i][0].transcript);
    };
    recognizer.onerror = (event) => { const note = dictationProblem(event.error); if (note) this.events.problem(note); };
    recognizer.onend = () => {
      this.recognizer = null;
      this.events.listening(false);
      const text = spoken(this.phrases);
      this.phrases = [];
      if (text) this.events.heard(text);
    };
    this.recognizer = recognizer;
    this.events.problem("");
    this.events.listening(true);
    recognizer.start();
  }

  stop(): void {
    this.recognizer?.stop();
  }

  // dispose stops listening without typing anything, for a terminal going away.
  dispose(): void {
    const recognizer = this.recognizer;
    this.recognizer = null;
    this.phrases = [];
    if (!recognizer) return;
    recognizer.onresult = null;
    recognizer.onerror = null;
    recognizer.onend = null;
    recognizer.abort();
  }
}
