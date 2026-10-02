import { useLayoutEffect, useRef } from "react";
import { createPortal } from "react-dom";

// The room a tip leaves between itself and its card, and at the screen's edges.
const GAP = 8;

// The tip of a part of a task card. It floats over the whole page, over the
// card or under it, whichever edge the part is nearer, or on the other side
// when the screen has no room there, centered on the part and kept on the
// screen, so it never covers the card's own controls or links. It is placed
// before it is first drawn.
export function CardTip({ text, part, card }: { text: string; part: HTMLElement; card: HTMLElement }) {
  const tip = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const node = tip.current!, own = node.getBoundingClientRect(), shell = card.getBoundingClientRect(), anchor = part.getBoundingClientRect();
    const { clientWidth: width, clientHeight: height } = document.documentElement;
    const under = shell.bottom + GAP, over = shell.top - GAP - own.height;
    const fitsUnder = under + own.height <= height - GAP, fitsOver = over >= GAP;
    const isLow = anchor.top + anchor.height / 2 > shell.top + shell.height / 2;
    const top = (isLow && fitsUnder) || !fitsOver ? under : over;
    node.style.left = Math.max(GAP, Math.min(anchor.left + anchor.width / 2 - own.width / 2, width - GAP - own.width)) + "px";
    node.style.top = Math.max(GAP, Math.min(top, height - GAP - own.height)) + "px";
  }, [text, part, card]);
  return createPortal(<div ref={tip} className="card-tip" role="tooltip">{text}</div>, document.body);
}
