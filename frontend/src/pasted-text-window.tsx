import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Icon } from "./Icon";

// A long paste a message carries, in a window over the board under its name:
// every line of its text as he pasted it, read back whole from where the
// board kept it (source), scrolling inside the window. Escape or Close puts
// the window away and returns the keyboard to what opened it. Nothing it
// holds reaches the message under it, whose click shows its Copy row.
export function PastedTextWindow({ name, source, onClose }: { name: string; source: string; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [text, setText] = useState<string | null>(null);
  const [isUnreadable, setUnreadable] = useState(false);
  useLayoutEffect(() => {
    const opened = dialog.current;
    opened?.showModal();
    return () => opened?.close();
  }, []);
  useEffect(() => {
    const reading = new AbortController();
    fetch(source, { signal: reading.signal })
      .then((response) => response.ok ? response.text() : Promise.reject(new Error("Read failed (" + response.status + ")")))
      .then(setText, () => { if (!reading.signal.aborted) setUnreadable(true); });
    return () => reading.abort();
  }, [source]);
  return createPortal(<dialog ref={dialog} className="pasted-text-window" aria-label={name}
    onCancel={(event) => { event.preventDefault(); onClose(); }} onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
    <header>
      <h2>{name}</h2>
      <button type="button" className="icon-button" aria-label="Close" onClick={onClose}><Icon name="close" /></button>
    </header>
    {text !== null ? <pre tabIndex={0}>{text}</pre> : <p>{isUnreadable ? "This paste is no longer available." : "Reading the paste…"}</p>}
  </dialog>, document.body);
}
