import { useEffect, useState } from "react";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";

const COPIED_MS = 1400;

// CodeCopy is a code card's Copy, its icon alone (#2524: "You don't need the
// word copy, we just have the icon"): label names it for a reader, and it
// shows a check and reads Copied for a moment once all of text is on the
// clipboard.
export function CodeCopy({ text, label = "Copy" }: { text: string; label?: string }) {
  const [isCopied, setCopied] = useState(false);
  useEffect(() => {
    if (!isCopied) return;
    const timer = setTimeout(() => setCopied(false), COPIED_MS);
    return () => clearTimeout(timer);
  }, [isCopied]);
  return <button type="button" className="code-copy" aria-label={isCopied ? "Copied" : label} onClick={() => { copyText(text).then(() => setCopied(true), () => {}); }}>
    <Icon name={isCopied ? "check" : "copy"} />
  </button>;
}
