import { useEffect, useState } from "react";
import { message } from "./api";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";
import { Notice } from "./notice";

const COPIED_MS = 1400;

// CodeBlock is a code block in a message, exactly as written, in a rounded
// card under a quiet header with the language its fence names, a Copy that
// says Copied for a moment once the code is on the clipboard, and Expand. The
// card shows its first lines and scrolls a long line sideways inside itself,
// so nothing runs off a phone's side; Expand opens it whole, every line
// wrapped. textClass names its text, as a call's result does.
// Given onRun, a PowerShell command in a reply's own words also has Run beside
// Copy (#2051), which asks the board to run the block in a terminal of its
// own; it waits while that terminal starts, so a second press runs nothing,
// and says why when the board refused.
export function CodeBlock({ text, language, textClass, onRun }: { text: string; language: string; textClass?: string; onRun?: () => Promise<void> }) {
  const [isCopied, setCopied] = useState(false);
  const [isExpanded, setExpanded] = useState(false);
  const [isStarting, setStarting] = useState(false);
  const [refused, setRefused] = useState("");
  const run = async () => {
    if (!onRun || isStarting) return;
    setStarting(true);
    setRefused("");
    try {
      await onRun();
    } catch (error: unknown) {
      setRefused(message(error));
    } finally {
      setStarting(false);
    }
  };
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
      {onRun && <button type="button" className="code-copy code-run" disabled={isStarting} onClick={() => void run()}>
        <Icon name={isStarting ? "clock" : "play"} />{isStarting ? "Starting" : "Run"}
      </button>}
      <button type="button" className="icon-button code-expand" aria-label={isExpanded ? "Collapse" : "Expand"} aria-expanded={isExpanded}
        onClick={() => setExpanded((prior) => !prior)}><Icon name={isExpanded ? "restore" : "maximize"} /></button>
    </figcaption>
    {refused && <Notice tone="warning">{refused}</Notice>}
    <pre className={textClass}><code>{text}</code></pre>
  </figure>;
}
