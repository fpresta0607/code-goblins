import { Fragment } from "react";
import { inlineElements, messageElements } from "./messageText";
import { textParts } from "./session-messages";
import "./message-values.css";

// A message's words as Claude Code wrote them: its code blocks as written,
// its headings in bold, and the prose between them with messageText's bold,
// code, lists, tables and links, so markup in a message is shown, never run.
export function MessageText({ text }: { text: string }) {
  return <div className="message-words">{textParts(text).map((part, at) => part.kind === "code"
    ? <pre key={at} className="message-code"><code>{part.text}</code></pre>
    : part.kind === "heading" ? <p key={at} className="message-heading">{inlineElements(part.text)}</p>
      : <Fragment key={at}>{messageElements(part.text)}</Fragment>)}</div>;
}
