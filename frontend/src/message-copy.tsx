import { useEffect, useState } from "react";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";

const COPIED_MS = 1400;

// MessageCopy is a message's Copy, or one text of a reply's: it copies text
// as written and shows a check for a moment once it is on the clipboard.
export function MessageCopy({ text }: { text: string }) {
  const [isCopied, setCopied] = useState(false);
  useEffect(() => {
    if (!isCopied) return;
    const timer = setTimeout(() => setCopied(false), COPIED_MS);
    return () => clearTimeout(timer);
  }, [isCopied]);
  return <button type="button" className="icon-button message-copy" aria-label="Copy"
    onClick={(event) => { event.stopPropagation(); copyText(text).then(() => setCopied(true), () => {}); }}><Icon name={isCopied ? "check" : "copy"} /></button>;
}
