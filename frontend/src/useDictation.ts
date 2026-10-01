import { useCallback, useEffect, useRef, useState } from "react";
import { Dictation, dictationKey, openMicrophone, speechRecognition } from "./dictation";

// A note about dictation stays this long over the terminal.
const NOTE_MS = 6000;

// Push-to-talk dictation for one terminal. Its key handler hands every key to
// key first: false means the terminal must not see the key, true that it may,
// and null that the key is not dictation's. What was heard is typed with type.
// deferTo answers, at each press, whether another recorder owns the shortcut
// (SIQspeak, when it runs), so one press never starts two recorders. level
// reads the microphone while listening, for the voice bubble's waveform.
export function useDictation(type: (text: string) => void, deferTo?: () => Promise<boolean>) {
  const [listening, setListening] = useState(false);
  const [note, setNote] = useState("");
  const typeText = useRef(type);
  const defer = useRef(deferTo);
  useEffect(() => { typeText.current = type; defer.current = deferTo; });
  const dictation = useRef<Dictation | null>(null);
  // presses counts presses and releases, so a check that answers after the
  // keys were let go starts nothing.
  const presses = useRef(0);
  useEffect(() => () => { dictation.current?.dispose(); dictation.current = null; }, []);
  useEffect(() => {
    if (!note) return;
    const timer = setTimeout(() => setNote(""), NOTE_MS);
    return () => clearTimeout(timer);
  }, [note]);
  useEffect(() => {
    if (!listening) return;
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
  }, [listening]);
  const key = useCallback((event: KeyboardEvent): boolean | null => {
    const meaning = dictationKey(event);
    if (!meaning) return null;
    if (meaning.swallow) event.preventDefault();
    if (meaning.action === "start") {
      const press = ++presses.current;
      const begin = () => {
        if (press !== presses.current) return;
        dictation.current ??= new Dictation({ heard: (text) => typeText.current(text), listening: setListening, problem: setNote }, speechRecognition, navigator.language || "en-US", openMicrophone);
        dictation.current.start();
      };
      const check = defer.current;
      if (!check) begin();
      else check().then((deferred) => { if (!deferred) begin(); }, begin);
    }
    if (meaning.action === "stop") {
      presses.current++;
      dictation.current?.stop();
    }
    return !meaning.swallow;
  }, []);
  const level = useCallback(() => dictation.current?.level() ?? 0, []);
  return { listening, note, key, level };
}
