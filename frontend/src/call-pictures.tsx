import { useCallback, useContext, useState } from "react";
import type { CallPicture } from "./conversation-pictures";
import { PictureHoverCard } from "./picture-hover-card";
import { PictureImage } from "./picture-image";
import { PictureOpener } from "./picture-opener";

// The most thumbnails a row shows; the rest are counted beside them, so a
// run of screenshots never crowds the row's words off a phone.
const SHOWN = 3;

// The pictures a row's calls read, took or produced, as small thumbnails at
// the row's end, as Claude's app shows them. Each loads only as its row nears
// the screen and keeps its size, so nothing moves when it arrives. A mouse
// over one shows it larger in a card with its call's verb and its place among
// the row's pictures; a click or a tap opens the conversation's viewer on it,
// and never opens or closes the row.
export function CallPictures({ pictures }: { pictures: CallPicture[] }) {
  const open = useContext(PictureOpener);
  const [hovered, setHovered] = useState<{ at: number; anchor: DOMRect } | null>(null);
  const dismiss = useCallback(() => setHovered(null), []);
  if (!pictures.length) return null;
  const rest = pictures.length - SHOWN;
  const view = (src: string) => (event: { preventDefault: () => void; stopPropagation: () => void }) => {
    event.preventDefault();
    event.stopPropagation();
    setHovered(null);
    open?.(src);
  };
  return <span className="call-pictures" onPointerLeave={dismiss}>
    {pictures.slice(0, SHOWN).map((each, at) => <button key={each.src} type="button" className="call-picture" aria-label={"Open picture " + (at + 1) + " of " + pictures.length + ", " + each.verb}
      onPointerEnter={(event) => { if (event.pointerType !== "touch") setHovered({ at, anchor: event.currentTarget.getBoundingClientRect() }); }}
      onClick={view(each.src)}>
      <PictureImage src={each.src} alt="" isLazy />
    </button>)}
    {rest > 0 && <button type="button" className="call-picture more" aria-label={"Open " + rest + " more " + (rest === 1 ? "picture" : "pictures")} onPointerEnter={dismiss} onClick={view(pictures[SHOWN].src)}>+{rest}</button>}
    {hovered && hovered.at < pictures.length && <PictureHoverCard picture={pictures[hovered.at]} place={hovered.at + 1} count={pictures.length} anchor={hovered.anchor} onDismiss={dismiss} />}
  </span>;
}
