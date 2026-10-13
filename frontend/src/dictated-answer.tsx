import { appendDictation, useDictation } from "./useDictation";
import { DictationButton } from "./dictation-button";
import "./dictated-answer.css";

// The box he writes his own answer in, which dictates as the message box under
// a goblin does: the microphone sits inside the box at its bottom right
// corner, and Ctrl+Shift+Space dictates from the keyboard. What he says joins
// what he wrote, after a space.
export function DictatedAnswer({ label, placeholder, value, instance, disabled, onText }: {
  label: string; placeholder: string; value: string; instance: string; disabled?: boolean; onText: (text: string) => void;
}) {
  const dictation = useDictation((text) => onText(appendDictation(value, text)), instance);
  return <div className="written-answer">
    <div className="dictated-box">
      <textarea aria-label={label} rows={3} maxLength={4000} placeholder={placeholder} value={value} disabled={disabled}
        onChange={(event) => onText(event.target.value)}
        onKeyDown={(event) => { dictation.key(event.nativeEvent); }}
        onKeyUp={(event) => { dictation.key(event.nativeEvent); }} />
      <DictationButton dictation={dictation} disabled={disabled} />
    </div>
    {dictation.note && <p className="message-note" role="status">{dictation.note}</p>}
  </div>;
}
