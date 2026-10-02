import { useCallback, useEffect, useRef, useState } from "react";
import { Dictation, dictationKey, openMicrophone } from "./dictation";
import { modelName, recognizerFor } from "./dictationEngine";

// A note about dictation stays this long over the terminal.
const NOTE_MS = 6000;

// Push-to-talk dictation for one terminal. Its key handler hands every key to
// key first: false means the terminal must not see the key, true that it may,
// and null that the key is not dictation's. What was heard is typed with type.
// level reads the microphone while listening, for the voice bubble's waveform.
// instance is the board's token, which the supervisor's speech model is asked
// with, and model is that model's name once the supervisor has said it.
export function useDictation(type: (text: string) => void, instance: string) {
  const [listening, setListening] = useState(false);
  const [note, setNote] = useState("");
  const [model, setModel] = useState("");
  const typeText = useRef(type);
  const token = useRef(instance);
  useEffect(() => { typeText.current = type; token.current = instance; });
  useEffect(() => {
    let live = true;
    void modelName().then((name) => { if (live) setModel(name); });
    return () => { live = false; };
  }, []);
  const dictation = useRef<Dictation | null>(null);
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
      dictation.current ??= new Dictation({ heard: (text) => typeText.current(text), listening: setListening, problem: setNote }, () => recognizerFor(() => token.current), navigator.language || "en-US", openMicrophone);
      dictation.current.start();
    }
    if (meaning.action === "stop") {
      dictation.current?.stop();
    }
    return !meaning.swallow;
  }, []);
  const level = useCallback(() => dictation.current?.level() ?? 0, []);
  return { listening, note, key, level, model };
}
