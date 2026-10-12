import { useEffect, useRef, useState } from "react";
import { ClickFeedback } from "./click-feedback";

const DOT = "•";

// A field for one credential value that never holds the value. What he pastes
// or types is taken from the edit before it reaches the field and handed to
// the card, and the field is given one dot for each character. A browser
// offers to save what a password field held, and Edge keeps what any text
// field held, even one drawn as dots with autocomplete off: here neither has
// anything but dots to keep. Copying out of the field copies dots. The card
// hears whether a value came from a paste, which it stores at once, and Enter
// asks it to store what was typed.
export function CredentialField({ label, placeholder, disabled, onValue, onCommit }: {
  label: string; placeholder: string; disabled: boolean; onValue: (value: string, pasted: boolean) => void; onCommit: () => void;
}) {
  const field = useRef<HTMLInputElement>(null);
  const hand = useRef(onValue);
  const commit = useRef(onCommit);
  const [refused, setRefused] = useState(false);
  useEffect(() => { hand.current = onValue; commit.current = onCommit; });

  useEffect(() => {
    const input = field.current;
    if (!input) return;
    let value = "";
    // Where a composition began: its text reaches the field, and is taken out when it ends.
    let composing: { start: number; end: number } | null = null;
    const set = (next: string, caret: number, pasted = false) => {
      value = next;
      input.value = DOT.repeat(next.length);
      input.setSelectionRange(caret, caret);
      hand.current(next, pasted);
      setRefused(false);
    };
    // A field takes no line breaks; one pasted with a value is left out.
    const plain = (text: string) => text.replace(/[\r\n]/g, "");
    const edit = (event: InputEvent) => {
      if (event.isComposing || !event.cancelable) return;
      event.preventDefault();
      const start = input.selectionStart ?? 0, end = input.selectionEnd ?? 0;
      const kind = event.inputType;
      let from = start, to = end, text = "";
      if (kind.startsWith("insert")) {
        text = plain(event.data ?? event.dataTransfer?.getData("text/plain") ?? "");
        if (!text) return;
        if (text.includes(DOT)) return setRefused(true);
      } else if (!kind.startsWith("delete")) return;
      else if (start === end) {
        if (kind === "deleteContentBackward") from = Math.max(0, start - 1);
        else if (kind === "deleteContentForward") to = Math.min(value.length, end + 1);
        else if (kind.endsWith("Backward")) from = 0;
        else if (kind.endsWith("Forward")) to = value.length;
      }
      set(value.slice(0, from) + text + value.slice(to), from + text.length, kind === "insertFromPaste" || kind === "insertFromDrop");
    };
    // Text that reached the field itself, from a composition or from
    // something that filled it, is taken out of it at once. A fill replaces
    // the value; one that left dots in the field cannot be read, so the field
    // is emptied rather than guessed at.
    const settle = (event: Event) => {
      if (event instanceof InputEvent && event.isComposing) return;
      // A composition is over by now, whether it left text or was abandoned.
      const composed = composing;
      composing = null;
      const shown = input.value;
      if (shown === DOT.repeat(value.length)) return;
      if (!composed && shown.includes(DOT)) return set("", 0);
      const { start, end } = composed ?? { start: 0, end: value.length };
      const text = plain(shown.slice(start, start + shown.length - (value.length - (end - start))));
      set(value.slice(0, start) + text + value.slice(end), start + text.length);
    };
    const begin = () => { composing = { start: input.selectionStart ?? 0, end: input.selectionEnd ?? 0 }; };
    const enter = (event: KeyboardEvent) => { if (event.key === "Enter" && !event.isComposing) { event.preventDefault(); commit.current(); } };
    input.addEventListener("beforeinput", edit);
    input.addEventListener("keydown", enter);
    input.addEventListener("input", settle);
    input.addEventListener("compositionstart", begin);
    input.addEventListener("compositionend", settle);
    return () => {
      input.removeEventListener("beforeinput", edit);
      input.removeEventListener("keydown", enter);
      input.removeEventListener("input", settle);
      input.removeEventListener("compositionstart", begin);
      input.removeEventListener("compositionend", settle);
    };
  }, []);

  return <>
    <input ref={field} className="credential-input" type="text" placeholder={placeholder} disabled={disabled} autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" data-1p-ignore="" data-lpignore="true" aria-label={label} />
    <ClickFeedback text={refused ? "That paste was the hidden dots, not the value." : ""} />
  </>;
}
