import { useRef, type PointerEvent } from "react";
import { Icon } from "./Icon";
import { TOUCH_SCREEN } from "./small-screen";
import type { useDictation } from "./useDictation";
import { VoiceBars } from "./voice-bars";

// A press shorter than this is a click, not a dictation.
const CLICK_MS = 300;

// The microphone a message box or an answer box holds to dictate. It listens
// while it is held, by pointer or by key, and keeps the keyboard in the box.
// Given onClick, a pointer's quick press is a click instead, whose hold is
// cancelled unheard, as the terminal's bubble opens the recent dictations on
// a click (the Overlord, 2026-10-28: "what happened to being able to click
// the microphone and see recent messages"). On a touch screen, where nothing
// is held down with ease, a tap starts it and the next tap stops it and adds
// what was said to the box.
export function DictationButton({ dictation, disabled, onClick }: { dictation: ReturnType<typeof useDictation>; disabled?: boolean; onClick?: () => void }) {
  const pressedAt = useRef(0);
  const hold = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    pressedAt.current = event.timeStamp;
    dictation.start(event.timeStamp);
  };
  const release = (event: PointerEvent<HTMLButtonElement>) => {
    if (!onClick || event.timeStamp - pressedAt.current >= CLICK_MS) {
      dictation.stop();
      return;
    }
    dictation.cancel();
    onClick();
  };
  if (matchMedia(TOUCH_SCREEN).matches) {
    return <button type="button" className={"voice-bubble" + (dictation.listening ? " recording" : "")} aria-label={dictation.listening ? "Stop dictating" : "Dictate"}
      aria-pressed={dictation.listening} data-tip={dictation.listening ? "Tap to add what you said" : "Tap to dictate"} disabled={disabled}
      onPointerDown={(event) => event.preventDefault()} onClick={(event) => dictation.toggle(event.timeStamp)}>
      {dictation.listening ? <VoiceBars level={dictation.level} /> : <Icon name="mic" />}
    </button>;
  }
  return <button type="button" className={"voice-bubble" + (dictation.listening ? " recording" : "")} aria-label={dictation.listening ? "Listening" : "Hold to dictate"}
    data-tip={dictation.listening ? "Release to add what you said" : "Hold to dictate"} disabled={disabled}
    onPointerDown={hold} onPointerUp={release} onPointerCancel={dictation.stop}
    onKeyDown={(event) => { if (event.key === " " || event.key === "Enter") { event.preventDefault(); dictation.start(event.timeStamp); } }}
    onKeyUp={(event) => { if (event.key === " " || event.key === "Enter") dictation.stop(); }}>
    {dictation.listening ? <VoiceBars level={dictation.level} /> : <Icon name="mic" />}
  </button>;
}
