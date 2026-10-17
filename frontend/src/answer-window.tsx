import { useEffect, useId, useRef, useState } from "react";
import { message, request } from "./api";
import { DictatedAnswer } from "./dictated-answer";
import { Icon } from "./Icon";
import { object, string } from "./types";
import "./credential-card.css";

interface Picture { key: number; preview: string; path: string }

// isSending holds Send while the board takes the answer, and refusal is why
// the board refused the last one.
export function AnswerWindow({ asker, task, instance, isSending = false, refusal: refused = "", onCancel, onSend }: { asker: string; task: string; instance: string; isSending?: boolean; refusal?: string; onCancel: () => void; onSend: (text: string) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  const [note, setNote] = useState("");
  const [pictures, setPictures] = useState<Picture[]>([]);
  const [refusal, setRefusal] = useState("");
  const pasted = useRef(0);
  useEffect(() => {
    const element = dialog.current;
    const source = document.activeElement;
    element?.showModal();
    element?.querySelector("textarea")?.focus();
    return () => { element?.close(); if (source instanceof HTMLElement && source.isConnected) source.focus(); };
  }, []);
  const keep = async (file: File) => {
    const key = ++pasted.current;
    setRefusal("");
    setPictures((prior) => [...prior, { key, preview: "", path: "" }]);
    try {
      // The board's CSP permits data URLs for images, but not blob URLs.
      const previewReady = new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = () => reject(reader.error);
        reader.readAsDataURL(file);
      });
      const [preview, kept] = await Promise.all([previewReady,
        request("/api/answer-images?task=" + encodeURIComponent(task), undefined, { method: "POST", headers: { "Content-Type": file.type, "X-CFO-Token": instance }, body: file })]);
      setPictures((prior) => prior.map((each) => each.key === key ? { key, preview, path: string(object(kept).path) } : each));
    } catch (error) {
      setRefusal(message(error));
      setPictures((prior) => prior.filter((each) => each.key !== key));
    }
  };
  const text = [note.trim(), pictures.map((picture) => "Image: " + picture.path).join("\n")].filter(Boolean).join("\n\n");
  const isKeeping = pictures.some((picture) => !picture.path);
  const isTooLong = new TextEncoder().encode(text).length > 4000;
  return <dialog ref={dialog} className="credential-replace skip-note" aria-labelledby={title}
    onKeyDown={(event) => { if (event.key === "Escape") event.stopPropagation(); }} onCancel={(event) => { event.preventDefault(); onCancel(); }}
    onPaste={(event) => {
      const images = Array.from(event.clipboardData.files).filter((file) => file.type.startsWith("image/"));
      if (!images.length) return;
      event.preventDefault();
      images.forEach((file) => void keep(file));
    }}>
    <h2 id={title}>Something else?</h2>
    <p>It goes to {asker} as your word. Type it, say it or paste a picture.</p>
    <DictatedAnswer label="Your note" placeholder={"Tell " + asker + " what to do instead..."} value={note} instance={instance} onText={setNote} />
    {pictures.length > 0 && <div className="pasted-pictures">
      {pictures.map((picture, index) => <span key={picture.key} className="pasted-picture">
        {picture.preview && <img src={picture.preview} alt={"Pasted picture " + (index + 1)} />}
        <button type="button" className="icon-button" aria-label={"Remove picture " + (index + 1)} onClick={() => setPictures((prior) => prior.filter((each) => each.key !== picture.key))}><Icon name="close" /></button>
      </span>)}
    </div>}
    {isKeeping && <p className="message-note" role="status">Saving picture...</p>}
    {refusal && <p className="message-note" role="status">{refusal}</p>}
    {isTooLong && <p className="message-note" role="status">Use a shorter answer or fewer pictures.</p>}
    {refused && <p className="message-note" role="status">{refused}</p>}
    <div className="credential-replace-choices">
      <button type="button" className="credential-replace-cancel" onClick={onCancel}><Icon name="close" />Cancel</button>
      <button type="button" className="primary" disabled={!text || isKeeping || isTooLong || isSending} onClick={() => onSend(text)}><Icon name="send" />Send</button>
    </div>
  </dialog>;
}
