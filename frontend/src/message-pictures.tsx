import { useContext, useState } from "react";
import { PictureImage } from "./picture-image";
import { PictureOpener } from "./picture-opener";
import { PictureViewer } from "./picture-viewer";

// The pictures a prompt or a call's result holds, by the address each is read
// back from, drawn in the message as thumbnails no wider than the list. Each
// loads only as its row nears the screen, and once loaded changes nothing
// but itself; one that cannot be read is a quiet mark of its own size. A
// click opens them larger: in a conversation's row, in that conversation's
// viewer, among every picture it holds, and anywhere else, as in a message
// still queued, among the message's own.
export function MessagePictures({ sources }: { sources: string[] }) {
  const openInConversation = useContext(PictureOpener);
  const [own, setOwn] = useState<string | null>(null);
  if (!sources.length) return null;
  return <div className="message-pictures">
    {sources.map((src, at) => <button key={src} type="button" className="message-picture" aria-label={"Open picture " + (at + 1)}
      onClick={(event) => { event.stopPropagation(); (openInConversation ?? setOwn)(src); }}>
      <PictureImage src={src} alt={"Picture " + (at + 1)} isLazy>Picture not available</PictureImage>
    </button>)}
    {own !== null && <PictureViewer sources={sources} source={own} onSource={setOwn} onClose={() => setOwn(null)} />}
  </div>;
}
