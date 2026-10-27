import { useEffect, useState } from "react";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";

const COPIED_MS = 1400;

// CodeBlock is a code block in a message, exactly as written, under a quiet
// header with the language its fence names and a Copy that says Copied for a
// moment once the code is on the clipboard.
export function CodeBlock({ text, language }: { text: string; language: string }) {
  const [isCopied, setCopied] = useState(false);
  useEffect(() => {
    if (!isCopied) return;
    const timer = setTimeout(() => setCopied(false), COPIED_MS);
    return () => clearTimeout(timer);
  }, [isCopied]);
  return <figure className="message-code">
    <figcaption>
      <span>{language}</span>
      <button type="button" className="code-copy" onClick={() => { copyText(text).then(() => setCopied(true), () => {}); }}>
        <Icon name={isCopied ? "check" : "copy"} />{isCopied ? "Copied" : "Copy"}
      </button>
    </figcaption>
    <pre><code>{text}</code></pre>
  </figure>;
}
