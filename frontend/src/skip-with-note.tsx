import { useEffect, useId, useRef, useState } from "react";
import { Icon } from "./Icon";
import { DictatedAnswer } from "./dictated-answer";
import "./credential-card.css";

// The pencil beside the decision button of a card that has no choices or
// answer box of its own, such as a command to run, a goblin's wait or a
// credential card. It opens a small window where he may type or say a note
// before the card clears: with a note it is his answer, which reaches whoever
// asked as his word, and with none it is Skip, and the asker is told to go on
// without it.
export function SkipWithNote({ asker, instance, disabled, onSkip, onAnswer }: {
  asker: string; instance: string; disabled: boolean; onSkip: () => void; onAnswer: (text: string) => void;
}) {
  const [isOpen, setIsOpen] = useState(false);
  return <>
    <button type="button" className="icon-button raised skip-with-note" aria-label="Skip or answer in your own words" data-tip={"Skip, or answer " + asker + " in your own words"} disabled={disabled} onClick={() => setIsOpen(true)}><Icon name="edit" /></button>
    {isOpen && <SkipNoteWindow asker={asker} instance={instance} onCancel={() => setIsOpen(false)}
      onSkip={(text) => { setIsOpen(false); if (text) onAnswer(text); else onSkip(); }} />}
  </>;
}

function SkipNoteWindow({ asker, instance, onCancel, onSkip }: { asker: string; instance: string; onCancel: () => void; onSkip: (text: string) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  const [note, setNote] = useState("");
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.querySelector("textarea")?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const text = note.trim();
  return <dialog ref={dialog} className="credential-replace skip-note" aria-labelledby={title}
    onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onCancel(); }}>
    <h2 id={title}>Skip this card?</h2>
    <p>{asker.charAt(0).toUpperCase() + asker.slice(1)} goes on without it. Add a note and it goes to {asker} as your answer.</p>
    <DictatedAnswer label="Your note" placeholder="Optional note" value={note} instance={instance} onText={setNote} />
    <div className="credential-replace-choices">
      <button type="button" className="credential-replace-cancel" onClick={onCancel}><Icon name="close" />Cancel</button>
      <button type="button" className="credential-replace-confirm" onClick={() => onSkip(text)}><Icon name={text ? "send" : "next"} />{text ? "Send and skip" : "Skip"}</button>
    </div>
  </dialog>;
}
