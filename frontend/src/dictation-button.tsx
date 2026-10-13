import type { PointerEvent } from "react";
import { Icon } from "./Icon";
import type { useDictation } from "./useDictation";
import { VoiceBars } from "./voice-bars";

// The microphone a message box or an answer box holds to dictate. It listens
// while it is held, by pointer or by key, and keeps the keyboard in the box.
export function DictationButton({ dictation, disabled }: { dictation: ReturnType<typeof useDictation>; disabled?: boolean }) {
  const hold = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    dictation.start(event.timeStamp);
  };
  return <button type="button" className={"voice-bubble" + (dictation.listening ? " recording" : "")} aria-label={dictation.listening ? "Listening" : "Hold to dictate"}
    data-tip={dictation.listening ? "Release to add what you said" : "Hold to dictate"} disabled={disabled}
    onPointerDown={hold} onPointerUp={dictation.stop} onPointerCancel={dictation.stop}
    onKeyDown={(event) => { if (event.key === " " || event.key === "Enter") { event.preventDefault(); dictation.start(event.timeStamp); } }}
    onKeyUp={(event) => { if (event.key === " " || event.key === "Enter") dictation.stop(); }}>
    {dictation.listening ? <VoiceBars level={dictation.level} /> : <Icon name="mic" />}
  </button>;
}
