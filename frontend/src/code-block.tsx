import { useEffect, useState } from "react";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";

const COPIED_MS = 1400;

// CodeBlock is a code block in a message, exactly as written, in a rounded
// card under a quiet header with the language its fence names, a Copy that
// says Copied for a moment once the code is on the clipboard, and Expand. The
// card shows its first lines and scrolls a long line sideways inside itself,
// so nothing runs off a phone's side; Expand opens it whole, every line
// wrapped. textClass names its text, as a call's result does.
export function CodeBlock({ text, language, textClass }: { text: string; language: string; textClass?: string }) {
  const [isCopied, setCopied] = useState(false);
  const [isExpanded, setExpanded] = useState(false);
  useEffect(() => {
    if (!isCopied) return;
    const timer = setTimeout(() => setCopied(false), COPIED_MS);
    return () => clearTimeout(timer);
  }, [isCopied]);
  return <figure className={"message-code" + (isExpanded ? " expanded" : "")}>
    <figcaption>
      <span>{language}</span>
      <button type="button" className="code-copy" onClick={() => { copyText(text).then(() => setCopied(true), () => {}); }}>
        <Icon name={isCopied ? "check" : "copy"} />{isCopied ? "Copied" : "Copy"}
      </button>
      <button type="button" className="icon-button code-expand" aria-label={isExpanded ? "Collapse" : "Expand"} aria-expanded={isExpanded}
        onClick={() => setExpanded((prior) => !prior)}><Icon name={isExpanded ? "restore" : "maximize"} /></button>
    </figcaption>
    <pre className={textClass}><code>{text}</code></pre>
  </figure>;
}
