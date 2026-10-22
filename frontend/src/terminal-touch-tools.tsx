import { useRef, type PointerEvent } from "react";
import { Icon } from "./Icon";

// What a terminal's touch tools hand on: text to type, a picture to add, the
// press of Copy text, and a few words on what a press met.
export interface TouchToolEvents { text: (text: string) => void; image: (picture: Blob) => void; copy: () => void; problem: (note: string) => void }

// The tools a terminal shows beside its microphone on a touch screen, for
// what a mouse and keys do at the desk (the Overlord, 2026-10-22, "i cant
// paste", "theres no copy and paste of screenshots or adding of attached
// images"). Paste types the clipboard's text or adds its picture, Add a
// picture adds one from the photo library or the camera, and Copy text shows
// the terminal's text to select and copy. A press keeps the keyboard where it
// is.
export function TerminalTouchTools({ events }: { events: TouchToolEvents }) {
  const picker = useRef<HTMLInputElement>(null);
  const keep = (event: PointerEvent) => event.preventDefault();
  // The clipboard is read in the press itself, which a phone asks to allow.
  const paste = async () => {
    if (!navigator.clipboard?.read) { events.problem("Hold the terminal, then tap Paste."); return; }
    try {
      for (const item of await navigator.clipboard.read()) {
        const picture = item.types.find((type) => type.startsWith("image/"));
        if (picture) { events.image(await item.getType(picture)); return; }
        if (item.types.includes("text/plain")) { events.text(await (await item.getType("text/plain")).text()); return; }
      }
      events.problem("Nothing to paste: the clipboard is empty.");
    } catch {
      events.problem("The clipboard was not read. Allow Paste when asked.");
    }
  };
  return <span className="terminal-touch-tools">
    <button type="button" className="icon-button" aria-label="Paste" data-tip="Paste" onPointerDown={keep} onClick={() => void paste()}><Icon name="paste" /></button>
    <button type="button" className="icon-button" aria-label="Add a picture" data-tip="Add a picture" onPointerDown={keep} onClick={() => picker.current?.click()}><Icon name="images" /></button>
    <input ref={picker} type="file" accept="image/*" hidden aria-label="Picture to add" onChange={(event) => {
      const picture = event.currentTarget.files?.[0];
      event.currentTarget.value = "";
      if (picture) events.image(picture);
    }} />
    <button type="button" className="icon-button" aria-label="Copy text" data-tip="Copy text" onPointerDown={keep} onClick={events.copy}><Icon name="copy" /></button>
  </span>;
}
