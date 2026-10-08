import { useCallback, useEffect, useRef, useState } from "react";
import { plainText, summary } from "./task-words";
import "./click-feedback.css";

// How long feedback on a click stays, and the most it says.
const SHOWN_MS = 6000;
const LONGEST = 60;

// useClickFeedback holds what his click on a control met, until it goes by
// itself or he clicks again.
export function useClickFeedback(): [string, (text: string) => void] {
  const [text, setText] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const show = useCallback((next: string) => {
    clearTimeout(timer.current);
    setText(next);
    if (next) timer.current = setTimeout(() => setText(""), SHOWN_MS);
  }, []);
  return [text, show];
}

// Feedback on the Overlord's own click: a few words beside what he clicked,
// which go by themselves. The persistent AFK switch refusal uses AfkRefusal
// instead.
export function ClickFeedback({ text }: { text: string }) {
  if (!text) return null;
  return <span className="click-feedback" role="status">{summary(plainText(text), LONGEST)}</span>;
}
