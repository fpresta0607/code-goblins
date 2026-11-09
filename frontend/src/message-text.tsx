import { Fragment } from "react";
import { CodeBlock } from "./code-block";
import { MessageDocument } from "./message-document";
import { inlineElements, messageElements } from "./messageText";
import { isPowerShellBlock, textParts } from "./session-messages";
import "./message-values.css";

const document = (label: string, target: string, key: number) => <MessageDocument key={key} label={label} target={target} />;

// A message's words as Claude Code wrote them: its code blocks as written,
// each with its language and a Copy, its headings in bold, and the prose
// between them with messageText's bold, code, lists, tables and links, so
// markup in a message is shown, never run. Given onRun, a reply's own words
// give each PowerShell block Run, which names the block by its place among
// the text's code blocks.
export function MessageText({ text, isThinking = false, onRun }: { text: string; isThinking?: boolean; onRun?: (fence: number) => Promise<void> }) {
  const documentCard = isThinking ? undefined : document;
  const parts = textParts(text);
  return <div className="message-words">{parts.map((part, at) => part.kind === "code"
    ? <CodeBlock key={at} text={part.text} language={part.language}
      onRun={onRun && isPowerShellBlock(part.language, part.text) ? () => onRun(parts.slice(0, at).filter((each) => each.kind === "code").length) : undefined} />
    : part.kind === "heading" ? <p key={at} className="message-heading">{inlineElements(part.text, undefined, undefined, documentCard)}</p>
      : <Fragment key={at}>{messageElements(part.text, undefined, undefined, documentCard)}</Fragment>)}</div>;
}
