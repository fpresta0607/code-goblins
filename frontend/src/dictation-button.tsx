import type { PointerEvent } from "react";
import { Icon } from "./Icon";
import { TOUCH_SCREEN } from "./small-screen";
import type { useDictation } from "./useDictation";
import { VoiceBars } from "./voice-bars";

// The microphone a message box or an answer box holds to dictate. It listens
// while it is held, by pointer or by key, and keeps the keyboard in the box.
// On a touch screen, where nothing is held down with ease, a tap starts it
// and the next tap stops it and adds what was said to the box.
export function DictationButton({ dictation, disabled, label }: { dictation: ReturnType<typeof useDictation>; disabled?: boolean; label?: boolean }) {
  const hold = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    dictation.start(event.timeStamp);
  };
  if (matchMedia(TOUCH_SCREEN).matches) {
    return <button type="button" className={"voice-bubble" + (label ? " composer-pill" : "") + (dictation.listening ? " recording" : "")} aria-label={dictation.listening ? "Stop dictating" : "Dictate"}
      aria-pressed={dictation.listening} data-tip={dictation.listening ? "Tap to add what you said" : "Tap to dictate"} disabled={disabled}
      onPointerDown={(event) => event.preventDefault()} onClick={(event) => dictation.toggle(event.timeStamp)}>
      {dictation.listening ? <VoiceBars level={dictation.level} /> : <Icon name="mic" />}
      {label && <span className="pill-label">{dictation.listening ? "Stop" : "Record"}</span>}
    </button>;
  }
  return <button type="button" className={"voice-bubble" + (label ? " composer-pill" : "") + (dictation.listening ? " recording" : "")} aria-label={dictation.listening ? "Listening" : "Hold to dictate"}
    data-tip={dictation.listening ? "Release to add what you said" : "Hold to dictate"} disabled={disabled}
    onPointerDown={hold} onPointerUp={dictation.stop} onPointerCancel={dictation.stop}
    onKeyDown={(event) => { if (event.key === " " || event.key === "Enter") { event.preventDefault(); dictation.start(event.timeStamp); } }}
    onKeyUp={(event) => { if (event.key === " " || event.key === "Enter") dictation.stop(); }}>
    {dictation.listening ? <VoiceBars level={dictation.level} /> : <Icon name="mic" />}
    {label && <span className="pill-label">{dictation.listening ? "Stop" : "Record"}</span>}
  </button>;
}
