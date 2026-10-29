import { useCallback, useEffect, useRef, useState } from "react";
import { reportToCfo } from "./api";
import { Dictation, dictationKey } from "./dictation";
import { dictationStatus, modelName, openMicrophone, prepareDictation, recognizerFor } from "./dictationEngine";
import { rememberDictation } from "./useVoice";

// A note about dictation stays this long over the terminal.
const NOTE_MS = 6000;

// While the first dictation sets up the speech model, the note asks the
// supervisor this often how far it is.
const SETUP_MS = 1000;

const READY = "Dictation is ready: hold Ctrl+Shift+Space and speak.";

// Keys that only modify another, as the shortcut's own Ctrl and Shift do.
const MODIFIERS = ["Control", "Shift", "Alt", "Meta"];

// A note over the terminal; a lasting one stays past NOTE_MS.
interface Note { text: string; lasting: boolean }

const NO_NOTE: Note = { text: "", lasting: false };

// What he dictates joins what he wrote, after a space.
export function appendDictation(prior: string, heard: string): string {
  return [prior.trimEnd(), heard.trim()].filter(Boolean).join(" ");
}

// Push-to-talk dictation for one terminal or message box. Its key handler
// hands every key to key first: false means the terminal must not see the
// key, true that it may, and null that the key is not dictation's. start and
// stop listen for as long as a microphone button is held, from at, when it
// went down, and toggle starts listening at one tap of a touch screen's
// microphone and stops at the next, to send the words. What was heard is
// typed with type, told whether it is sent, and joins the board's recent
// dictations, which every window lists.
// level reads the microphone while listening, for the voice bubble's waveform.
// instance is the board's token, which the supervisor's speech model is asked
// with, and model is that model's name once the supervisor has said it.
export function useDictation(type: (text: string, isSent: boolean) => void, instance: string) {
  const [listening, setListening] = useState(false);
  const [note, setNote] = useState<Note>(NO_NOTE);
  const [model, setModel] = useState("");
  const typeText = useRef(type);
  const token = useRef(instance);
  const live = useRef(true);
  useEffect(() => { typeText.current = type; token.current = instance; });
  useEffect(() => {
    live.current = true;
    return () => { live.current = false; };
  }, []);
  useEffect(() => {
    let named = true;
    void modelName().then((name) => { if (named) setModel(name); });
    prepareDictation();
    return () => { named = false; };
  }, []);
  const dictation = useRef<Dictation | null>(null);
  // A dictation typed right after another, with no key pressed in the
  // terminal between them, starts with a space, so the two do not run
  // together.
  const followsDictation = useRef(false);
  useEffect(() => () => { dictation.current?.dispose(); dictation.current = null; }, []);
  useEffect(() => {
    if (!note.text || note.lasting) return;
    const timer = setTimeout(() => setNote(NO_NOTE), NOTE_MS);
    return () => clearTimeout(timer);
  }, [note]);
  // follow asks the supervisor about its speech model after a dictation it
  // refused. While the first dictation sets the model up, the note follows
  // the download until the model is ready and then says so, and a set-up that
  // failed stays shown until the next dictation.
  const following = useRef(false);
  const follow = useCallback(async () => {
    if (following.current) return;
    following.current = true;
    let settingUp = false;
    try {
      for (;;) {
        const status = await dictationStatus().catch(() => null);
        if (!live.current || !status) return;
        if (status.state !== "fetching") {
          if (status.state === "missing" && status.note) setNote({ text: status.note, lasting: true });
          else if (status.state === "ready" && settingUp) setNote({ text: READY, lasting: false });
          return;
        }
        settingUp = true;
        setNote({ text: status.note, lasting: true });
        await new Promise((resolve) => setTimeout(resolve, SETUP_MS));
      }
    } finally {
      following.current = false;
    }
  }, []);
  // While it listens the screen is held awake. A dictation ends when the page
  // is hidden, and speaking touches nothing, so a phone's own idle timer
  // locked the screen mid-dictation and ended it where it was (the Overlord,
  // 2026-10-22, "it only records like the first few sentences", "It's like a
  // timer or something").
  useEffect(() => {
    if (!listening || !("wakeLock" in navigator)) return;
    let isDone = false, lock: WakeLockSentinel | null = null;
    navigator.wakeLock.request("screen").then((held) => { if (isDone) void held.release(); else lock = held; }, () => undefined);
    return () => { isDone = true; void lock?.release(); };
  }, [listening]);
  useEffect(() => {
    const stop = () => dictation.current?.stop();
    const release = (event: KeyboardEvent) => { if (dictationKey(event)?.action === "stop") stop(); };
    const hide = () => { if (document.hidden) stop(); };
    window.addEventListener("keyup", release, true);
    window.addEventListener("blur", stop);
    document.addEventListener("visibilitychange", hide);
    return () => {
      window.removeEventListener("keyup", release, true);
      window.removeEventListener("blur", stop);
      document.removeEventListener("visibilitychange", hide);
    };
  }, []);
  // start listens until stop, as holding the shortcut or a microphone button
  // does, from at, the time of the event that pressed it.
  const start = useCallback((at?: number) => {
    const heard = (text: string, isSent: boolean) => {
      typeText.current(followsDictation.current && !isSent ? " " + text : text, isSent);
      followsDictation.current = !isSent;
      rememberDictation(text, token.current);
    };
    const problem = (text: string) => {
      setNote({ text, lasting: false });
      if (text) void follow();
    };
    // Words that could not be had go to the CFO, not onto the board.
    const failed = (reason: string) => reportToCfo("dictation", reason);
    // A set-up that failed was shown until now.
    setNote((prior) => prior.lasting && !following.current ? NO_NOTE : prior);
    dictation.current ??= new Dictation({ heard, listening: setListening, problem, failed }, () => recognizerFor(() => token.current), navigator.language || "en-US", openMicrophone);
    dictation.current.start(at);
  }, [follow]);
  const stop = useCallback(() => dictation.current?.stop(), []);
  const cancel = useCallback(() => dictation.current?.cancel(), []);
  const toggle = useCallback((at: number) => {
    if (dictation.current?.isListening) dictation.current.stop(true);
    else start(at);
  }, [start]);
  const key = useCallback((event: KeyboardEvent): boolean | null => {
    const meaning = dictationKey(event);
    if (!meaning) {
      if (event.type === "keydown" && !MODIFIERS.includes(event.key)) followsDictation.current = false;
      return null;
    }
    if (meaning.swallow) event.preventDefault();
    if (meaning.action === "start") start(event.timeStamp);
    if (meaning.action === "stop") stop();
    return !meaning.swallow;
  }, [start, stop]);
  const level = useCallback(() => dictation.current?.level() ?? 0, []);
  return { listening, note: note.text, key, start, stop, cancel, toggle, level, model };
}
