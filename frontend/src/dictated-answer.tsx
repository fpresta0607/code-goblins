import { useEffect, useRef, useState, type ClipboardEvent, type DragEvent } from "react";
import { message } from "./api";
import { appendDictation, useDictation } from "./useDictation";
import { ComposerAttachments } from "./composer-attachments";
import { DictationButton } from "./dictation-button";
import { Icon } from "./Icon";
import { clipboardPicture, filesOf } from "./kept-files";
import { Notice } from "./notice";
import { TOUCH_SCREEN } from "./small-screen";
import { keepFile } from "./terminal-image";
import "./dictated-answer.css";

// The files a written answer carries: the paths the board kept them at, how
// many it is still keeping, which Send waits for, and the task whose goblin
// reads them, none for the CFO. Each change goes through a function of what
// the answer holds by then, since a file is kept after its upload ends.
export interface AnswerFiles {
  paths: string[]; adding: number; task?: string;
  onChange: (change: (prior: string[]) => string[]) => void; onAdding: (step: number) => void;
}

// The box he writes his own answer in, which dictates as the message box under
// a goblin does: the microphone sits inside the box at its bottom right
// corner, and Ctrl+Shift+Space dictates from the keyboard anywhere in the
// box's card or window, as from the Other he just chose, and puts the caret
// in the box.
// What he says joins what he wrote, after a space.
// Enter calls onSubmit, which the card guards as its own Send is, and Shift+Enter
// adds a line. An Enter that ends an input method's composition does neither.
// Given files, it takes a picture pasted into it, a file dropped on it and one
// picked with Add files at its bottom left corner, as the composer does, and
// shows each above his words with a button that takes it out. On a phone Add
// files opens Paste a picture, since a phone's own paste offers only text,
// and Photos or files.
export function DictatedAnswer({ label, placeholder, value, instance, disabled, files, onText, onSubmit }: {
  label: string; placeholder: string; value: string; instance: string; disabled?: boolean; files?: AnswerFiles; onText: (text: string) => void; onSubmit: () => void;
}) {
  const dictation = useDictation((text) => onText(appendDictation(value, text)), instance);
  const box = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const addControl = useRef<HTMLButtonElement>(null);
  const addMenu = useRef<HTMLDivElement>(null);
  const [isAddOpen, setAddOpen] = useState(false);
  const [refused, setRefused] = useState("");
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
  useEffect(() => {
    if (!isAddOpen) return;
    const away = (event: Event) => { if (event.target instanceof Node && !addMenu.current?.contains(event.target) && !addControl.current?.contains(event.target)) setAddOpen(false); };
    document.addEventListener("pointerdown", away, true);
    return () => document.removeEventListener("pointerdown", away, true);
  }, [isAddOpen]);
  const add = async (chosen: File[]) => {
    if (!files || !chosen.length) return;
    setRefused("");
    files.onAdding(chosen.length);
    for (const file of chosen) {
      try {
        const path = await keepFile(file, file.name || "pasted.png", instance, files.task);
        files.onChange((prior) => [...prior, path]);
      } catch (error: unknown) {
        setRefused("That " + (file.type.startsWith("image/") ? "picture" : "file") + " was not added: " + message(error));
      } finally {
        files.onAdding(-1);
      }
    }
  };
  const paste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const pictures = filesOf(event.clipboardData).filter((file) => file.type.startsWith("image/"));
    if (!files || !pictures.length) return;
    event.preventDefault();
    void add(pictures);
  };
  const dragOver = (event: DragEvent<HTMLDivElement>) => {
    if (!files || disabled || !event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  };
  const drop = (event: DragEvent<HTMLDivElement>) => {
    const dropped = filesOf(event.dataTransfer);
    if (!files || disabled || !dropped.length) return;
    event.preventDefault();
    void add(dropped);
  };
  const pastePicture = async () => {
    const read = await clipboardPicture();
    if ("picture" in read) await add([read.picture]);
    else setRefused(read.error);
  };
  const isTouch = matchMedia(TOUCH_SCREEN).matches;
  return <div className="written-answer" onDragOver={dragOver} onDrop={drop}>
    {files && files.paths.length > 0 && <ComposerAttachments paths={files.paths} instance={instance} task={files.task}
      onRemove={(path) => { files.onChange((prior) => prior.filter((each) => each !== path)); box.current?.focus(); }} />}
    <div className="dictated-box">
      <textarea ref={box} aria-label={label} rows={3} maxLength={4000} placeholder={placeholder} value={value} disabled={disabled}
        onChange={(event) => onText(event.target.value)} onPaste={paste}
        onKeyDown={(event) => {
          if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
          event.preventDefault();
          onSubmit();
        }} />
      {files && <>
        <input ref={picker} type="file" multiple hidden aria-hidden="true" tabIndex={-1}
          onChange={(event) => { const chosen = Array.from(event.target.files ?? []); event.target.value = ""; void add(chosen); }} />
        <button ref={addControl} type="button" className="answer-add" aria-label="Add files" aria-haspopup={isTouch ? "true" : undefined} aria-expanded={isTouch ? isAddOpen : undefined} disabled={disabled || files.adding > 0}
          onClick={() => { if (!isTouch) { picker.current?.click(); return; } setAddOpen((prior) => !prior); }}><Icon name="plus" /></button>
        {isAddOpen && <div ref={addMenu} className="answer-add-menu">
          <button type="button" onClick={() => { setAddOpen(false); void pastePicture(); }}><Icon name="paste" />Paste a picture</button>
          <button type="button" onClick={() => { setAddOpen(false); picker.current?.click(); }}><Icon name="images" />Photos or files</button>
        </div>}
      </>}
      <DictationButton dictation={dictation} disabled={disabled} />
    </div>
    {refused && <Notice form="box" tone="warning">{refused}</Notice>}
    {dictation.note && <Notice form="box" tone="warning">{dictation.note}</Notice>}
  </div>;
}
