import { useEffect, useLayoutEffect, useRef } from "react";
import type { CallPicture } from "./conversation-pictures";
import { PictureImage } from "./picture-image";

// The card's width, short of a narrow window's, the room it needs above its
// thumbnail before it opens under it instead, and how far it keeps from the
// thumbnail and from the window's edges.
const WIDTH_PX = 384;
const ROOM_PX = 300;
const GAP_PX = 8;

// The card a pointer over a row's thumbnail shows, as Claude's app does: the
// picture larger, with the verb of the call that holds it and its place among
// the row's pictures. It is drawn over the board, above its thumbnail or
// under it when the window has no room above, takes no pointer and no
// keyboard, and goes when the list scrolls or he presses Escape (onDismiss).
// That Escape is the card's alone: it reaches neither the panel, which it
// would close, nor the message box, where it would stop the turn. Its picture
// loads only once it shows.
export function PictureHoverCard({ picture, place, count, anchor, onDismiss }: {
  picture: CallPicture; place: number; count: number; anchor: DOMRect; onDismiss: () => void;
}) {
  const card = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => { card.current?.showPopover(); }, []);
  useEffect(() => {
    const keys = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      onDismiss();
    };
    window.addEventListener("scroll", onDismiss, true);
    window.addEventListener("keydown", keys, true);
    return () => {
      window.removeEventListener("scroll", onDismiss, true);
      window.removeEventListener("keydown", keys, true);
    };
  }, [onDismiss]);
  const width = Math.min(WIDTH_PX, window.innerWidth - 2 * GAP_PX);
  const left = Math.max(GAP_PX, Math.min(anchor.left + anchor.width / 2 - width / 2, window.innerWidth - GAP_PX - width));
  const edge = anchor.top < ROOM_PX + 2 * GAP_PX ? { top: anchor.bottom + GAP_PX } : { bottom: window.innerHeight - anchor.top + GAP_PX };
  return <div ref={card} popover="manual" className="picture-hover-card" aria-hidden="true" style={{ left, width, ...edge }}>
    <PictureImage src={picture.src} alt="">This image is no longer available.</PictureImage>
    <p><span>{picture.verb}</span><span>{place} of {count}</span></p>
  </div>;
}
