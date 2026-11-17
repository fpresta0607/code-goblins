import { useEffect, useId, useRef, useState } from "react";
import { message } from "./api";
import { DictatedAnswer, type AnswerFiles } from "./dictated-answer";
import { Icon } from "./Icon";
import { Notice } from "./notice";
import { keepFile } from "./terminal-image";
import "./credential-card.css";

// The most an answer is sent as written, in UTF-8 bytes: a card's answer is
// typed into its asker's terminal as one line, and a credential card takes no
// more. A longer one is kept as a text file the asker reads whole, line breaks
// and all, as the board keeps a picture, and the answer names it with its
// opening words (the Overlord, 2026-11-13: "It shouldn't have a character
// cap", #2538).
const LONGEST_WRITTEN = 4000;
const OPENING = 300;

// The answer that names the file holding a long note, with its opening words.
function keptAnswer(note: string, path: string): string {
  const opening = note.replace(/\s+/g, " ").slice(0, OPENING).trim();
  return "My whole answer is in " + path + ". It begins: " + opening + "...";
}

// His answer in his own words to a card with no answer box of its own, from
// the box the chat's own pictures and files go through: typed, said or pasted,
// with no cap on its length. isSending holds Send while the board takes the
// answer, and refusal is why the board refused the last one.
export function AnswerWindow({ asker, task, instance, isSending = false, refusal: refused = "", onCancel, onSend }: { asker: string; task: string; instance: string; isSending?: boolean; refusal?: string; onCancel: () => void; onSend: (text: string) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  const [note, setNote] = useState("");
  const [paths, setPaths] = useState<string[]>([]);
  const [adding, setAdding] = useState(0);
  const [isKeepingNote, setKeepingNote] = useState(false);
  const [refusal, setRefusal] = useState("");
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.querySelector("textarea")?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const files: AnswerFiles = { paths, adding, task: task || undefined, onChange: setPaths, onAdding: (step) => setAdding((count) => Math.max(0, count + step)) };
  const written = note.trim();
  const canSend = (!!written || paths.length > 0) && !adding && !isKeepingNote && !isSending;
  const send = async () => {
    if (!canSend) return;
    const whole = [written, ...paths].filter(Boolean).join("\n");
    if (new TextEncoder().encode(whole).length <= LONGEST_WRITTEN) { onSend(whole); return; }
    setKeepingNote(true);
    setRefusal("");
    try {
      const path = await keepFile(new Blob([written], { type: "text/plain" }), "answer.txt", instance, task || undefined);
      onSend([keptAnswer(written, path), ...paths].join("\n"));
    } catch (error: unknown) {
      setRefusal("Your answer was not sent: " + message(error));
    } finally {
      setKeepingNote(false);
    }
  };
  return <dialog ref={dialog} className="credential-replace skip-note" aria-labelledby={title}
    onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onCancel(); }}>
    <h2 id={title}>Something else?</h2>
    <p>It goes to {asker} as your word. Type it, say it or paste a picture.</p>
    <DictatedAnswer label="Your note" placeholder={"Tell " + asker + " what to do instead..."} value={note} instance={instance} files={files} onText={setNote} onSubmit={() => void send()} />
    {adding > 0 && <Notice form="box">Saving files...</Notice>}
    {refusal && <Notice form="box" tone="warning">{refusal}</Notice>}
    {refused && <Notice form="box" tone="warning">{refused}</Notice>}
    <div className="credential-replace-choices">
      <button type="button" className="credential-replace-cancel" onClick={onCancel}><Icon name="close" />Cancel</button>
      <button type="button" className="primary" disabled={!canSend} onClick={() => void send()}><Icon name="send" />Send</button>
    </div>
  </dialog>;
}
