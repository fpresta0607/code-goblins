import { useCallback, useEffect, useRef, useState } from "react";
import { Dictation, dictationKey, openMicrophone } from "./dictation";
import { dictationStatus, modelName, recognizerFor } from "./dictationEngine";

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

// Push-to-talk dictation for one terminal. Its key handler hands every key to
// key first: false means the terminal must not see the key, true that it may,
// and null that the key is not dictation's. What was heard is typed with type.
// level reads the microphone while listening, for the voice bubble's waveform.
// instance is the board's token, which the supervisor's speech model is asked
// with, and model is that model's name once the supervisor has said it.
export function useDictation(type: (text: string) => void, instance: string) {
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
  const key = useCallback((event: KeyboardEvent): boolean | null => {
    const meaning = dictationKey(event);
    if (!meaning) {
      if (event.type === "keydown" && !MODIFIERS.includes(event.key)) followsDictation.current = false;
      return null;
    }
    if (meaning.swallow) event.preventDefault();
    if (meaning.action === "start") {
      const heard = (text: string) => {
        typeText.current(followsDictation.current ? " " + text : text);
        followsDictation.current = true;
      };
      const problem = (text: string) => {
        setNote({ text, lasting: false });
        if (text) void follow();
      };
      // A set-up that failed was shown until now.
      setNote((prior) => prior.lasting && !following.current ? NO_NOTE : prior);
      dictation.current ??= new Dictation({ heard, listening: setListening, problem }, () => recognizerFor(() => token.current), navigator.language || "en-US", openMicrophone);
      dictation.current.start();
    }
    if (meaning.action === "stop") {
      dictation.current?.stop();
    }
    return !meaning.swallow;
  }, [follow]);
  const level = useCallback(() => dictation.current?.level() ?? 0, []);
  return { listening, note: note.text, key, level, model };
}
