import { useLayoutEffect, useRef, useState } from "react";
import { showMoreLabel } from "./cards";

// A goblin's own words, cut to a few lines with Show more while they run past
// them instead of ending in an ellipsis, and Show less once opened.
export function ShowMore({ text, className }: { text: string; className: string }) {
  const body = useRef<HTMLParagraphElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);
  // The observer reports the paragraph's size once when it starts and again
  // whenever the panel's width changes how far the text runs.
  useLayoutEffect(() => {
    const node = body.current;
    if (!node) return;
    const observer = new ResizeObserver(() => setOverflowing(node.scrollHeight > node.clientHeight + 1));
    observer.observe(node);
    return () => observer.disconnect();
  }, [text, expanded]);
  const label = showMoreLabel(expanded, overflowing);
  return <div className="show-more">
    <p ref={body} className={className + (expanded ? " expanded" : "")}>{text}</p>
    {label && <button className="show-more-toggle" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>{label}</button>}
  </div>;
}
