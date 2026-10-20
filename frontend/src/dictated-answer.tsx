import { useEffect, useRef } from "react";
import { appendDictation, useDictation } from "./useDictation";
import { DictationButton } from "./dictation-button";
import "./dictated-answer.css";

// The box he writes his own answer in, which dictates as the message box under
// a goblin does: the microphone sits inside the box at its bottom right
// corner, and Ctrl+Shift+Space dictates from the keyboard anywhere in the
// box's card or window, as from the Other he just chose, and puts the caret
// in the box.
// What he says joins what he wrote, after a space.
// Enter calls onSubmit, which the card guards as its own Send is, and Shift+Enter
// adds a line. An Enter that ends an input method's composition does neither.
export function DictatedAnswer({ label, placeholder, value, instance, disabled, onText, onSubmit }: {
  label: string; placeholder: string; value: string; instance: string; disabled?: boolean; onText: (text: string) => void; onSubmit: () => void;
}) {
  const dictation = useDictation((text) => onText(appendDictation(value, text)), instance);
  const box = useRef<HTMLTextAreaElement>(null);
  const { key } = dictation;
  useEffect(() => {
    const card = box.current?.closest<HTMLElement>("form, dialog");
    if (!card) return;
    const hear = (event: KeyboardEvent) => {
      if (key(event) === false && event.type === "keydown") box.current?.focus();
    };
    card.addEventListener("keydown", hear);
    card.addEventListener("keyup", hear);
    return () => {
      card.removeEventListener("keydown", hear);
      card.removeEventListener("keyup", hear);
    };
  }, [key]);
  return <div className="written-answer">
    <div className="dictated-box">
      <textarea ref={box} aria-label={label} rows={3} maxLength={4000} placeholder={placeholder} value={value} disabled={disabled}
        onChange={(event) => onText(event.target.value)}
        onKeyDown={(event) => {
          if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
          event.preventDefault();
          onSubmit();
        }} />
      <DictationButton dictation={dictation} disabled={disabled} />
    </div>
    {dictation.note && <p className="message-note" role="status">{dictation.note}</p>}
  </div>;
}
