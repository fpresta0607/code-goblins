import { useLayoutEffect, useRef } from "react";
import { createPortal } from "react-dom";

// The room a tip leaves between itself and its card, and at the screen's edges.
const GAP = 8;

// The tip of a part of a task card. It floats over the whole page, over the
// card or under it, whichever edge the part is nearer, or on the other side
// when the screen has no room there, centered on the part and kept on the
// screen, so it never covers the card's own controls or links. It is placed
// before it is first drawn, follows its card as the page scrolls or resizes,
// says what its part's data-tip says now, and hides while the part has no tip
// or has left the page.
export function CardTip({ part, card }: { part: HTMLElement; card: HTMLElement }) {
  const tip = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const node = tip.current!;
    const show = () => {
      const text = part.isConnected ? part.getAttribute("data-tip") : null;
      node.style.visibility = text ? "visible" : "hidden";
      if (!text) return;
      node.textContent = text;
      const own = node.getBoundingClientRect(), shell = card.getBoundingClientRect(), anchor = part.getBoundingClientRect();
      const { clientWidth: width, clientHeight: height } = document.documentElement;
      const under = shell.bottom + GAP, over = shell.top - GAP - own.height;
      const fitsUnder = under + own.height <= height - GAP, fitsOver = over >= GAP;
      const isLow = anchor.top + anchor.height / 2 > shell.top + shell.height / 2;
      const top = (isLow && fitsUnder) || !fitsOver ? under : over;
      node.style.left = Math.max(GAP, Math.min(anchor.left + anchor.width / 2 - own.width / 2, width - GAP - own.width)) + "px";
      node.style.top = Math.max(GAP, Math.min(top, height - GAP - own.height)) + "px";
    };
    show();
    addEventListener("scroll", show, { capture: true, passive: true });
    addEventListener("resize", show);
    const observer = new MutationObserver(show);
    observer.observe(card, { subtree: true, childList: true, attributes: true, attributeFilter: ["data-tip"] });
    return () => {
      removeEventListener("scroll", show, { capture: true });
      removeEventListener("resize", show);
      observer.disconnect();
    };
  }, [part, card]);
  return createPortal(<div ref={tip} className="card-tip" role="tooltip" />, document.body);
}
