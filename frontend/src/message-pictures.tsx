import { useState } from "react";
import { PictureViewer } from "./picture-viewer";

// The pictures a prompt or a call's result holds, by the address each is read
// back from, drawn in the message as thumbnails no wider than the list. Each
// loads only as its row nears the screen, and once loaded changes nothing
// but itself; a click opens them larger.
export function MessagePictures({ sources }: { sources: string[] }) {
  const [open, setOpen] = useState<number | null>(null);
  if (!sources.length) return null;
  return <div className="message-pictures">
    {sources.map((src, at) => <button key={src} type="button" className="message-picture" aria-label={"Open picture " + (at + 1)}
      onClick={(event) => { event.stopPropagation(); setOpen(at); }}>
      <img src={src} alt={"Picture " + (at + 1)} loading="lazy" decoding="async" />
    </button>)}
    {open !== null && <PictureViewer sources={sources} index={open} onIndex={setOpen} onClose={() => setOpen(null)} />}
  </div>;
}
