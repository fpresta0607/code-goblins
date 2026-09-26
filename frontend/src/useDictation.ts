import { useCallback, useEffect, useRef, useState } from "react";
import { Dictation, dictationKey, speechRecognition } from "./dictation";

// A note about dictation stays this long over the terminal.
const NOTE_MS = 6000;

// Push-to-talk dictation for one terminal. Its key handler hands every key to
// key first: false means the terminal must not see the key, true that it may,
// and null that the key is not dictation's. What was heard is typed with type.
export function useDictation(type: (text: string) => void) {
  const [listening, setListening] = useState(false);
  const [note, setNote] = useState("");
  const typeText = useRef(type);
  useEffect(() => { typeText.current = type; });
  const dictation = useRef<Dictation | null>(null);
  useEffect(() => () => { dictation.current?.dispose(); dictation.current = null; }, []);
  useEffect(() => {
    if (!note) return;
    const timer = setTimeout(() => setNote(""), NOTE_MS);
    return () => clearTimeout(timer);
  }, [note]);
  const key = useCallback((event: KeyboardEvent): boolean | null => {
    const meaning = dictationKey(event);
    if (!meaning) return null;
    if (meaning.swallow) event.preventDefault();
    if (meaning.action === "start") {
      dictation.current ??= new Dictation({ heard: (text) => typeText.current(text), listening: setListening, problem: setNote }, speechRecognition, navigator.language || "en-US");
      dictation.current.start();
    }
    if (meaning.action === "stop") dictation.current?.stop();
    return !meaning.swallow;
  }, []);
  return { listening, note, key };
}
