import { memo, useEffect, useRef, useState } from "react";
import { copyText as copyToClipboard } from "./clipboard";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { MessageCall } from "./message-call";
import { MessagePictures } from "./message-pictures";
import { MessageText } from "./message-text";
import { copyText, type SessionMessage } from "./session-messages";
import type { Task, TreeNode } from "./types";

const COPIED_MS = 1400;

// When a message was sent, short and whole: its date and time of day.
function sentAt(at: string): { exact: string; text: string } {
  const time = at && !at.startsWith("0001") ? new Date(at) : null;
  if (!time || Number.isNaN(time.getTime())) return { exact: "", text: "" };
  return {
    exact: time.toLocaleString(undefined, { weekday: "short", year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }),
    text: time.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }),
  };
}

// One message of a session as the Messages view draws it: a prompt in a
// bubble on the right, a slash or ! command as typed with what it printed; a
// reply on the left with its text, its thinking as readable quieter text and
// its calls; a compaction folded to its summary; an interrupt as a plain
// Interrupted row. What says who spoke is the side and the bubble, never a
// speaker line (the Overlord, 2026-10-26).
// Each of his own messages keeps a small row under it with Copy and when it
// was sent, hidden until he taps the message or points at it, and closed when
// he taps away. Its words stay selectable.
export const MessageRow = memo(function MessageRow({ message, task, picture, onChild }: {
  message: SessionMessage; task?: Task; picture: (id: string) => string; onChild: (child: TreeNode, source: HTMLElement) => void;
}) {
  const [copied, setCopied] = useState(false);
  const [isActionsOpen, setActionsOpen] = useState(false);
  const row = useRef<HTMLElement>(null);
  useEffect(() => {
    if (!isActionsOpen) return;
    const close = (event: PointerEvent) => {
      if (!row.current?.contains(event.target as Node)) setActionsOpen(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [isActionsOpen]);
  const words = copyText(message);
  const copy = words && <button type="button" className="icon-button message-copy" aria-label="Copy" data-tip={copied ? "Copied" : "Copy"}
    onClick={(event) => { event.stopPropagation(); copyToClipboard(words).then(() => { setCopied(true); setTimeout(() => setCopied(false), COPIED_MS); }, () => {}); }}><Icon name={copied ? "check" : "copy"} /></button>;
  if (message.kind === "interrupt") return <p className="message-interrupt" data-message={message.id}><Icon name="pause" />Interrupted</p>;
  if (message.kind === "compaction") return <div className="message-compaction" data-message={message.id}>
    {copy}
    <Disclosure kind="disclosure-line" title={message.trigger === "auto" ? "Compacted automatically" : "Compacted"}><MessageText text={message.text} /></Disclosure>
  </div>;
  if (message.kind === "reply") return <article className="message message-reply" data-message={message.id}>
    {copy}
    {message.blocks.map((block, at) => block.kind === "text" ? <MessageText key={at} text={block.text} />
      : block.kind === "thinking" ? block.text && <div key={at} className="message-thinking-read"><span className="thinking-label">Thinking</span><MessageText text={block.text} /></div>
        : <MessageCall key={at} call={block} task={task} picture={picture} onChild={onChild} />)}
  </article>;
  if (message.source === "notice") return <p className="message-notice" data-message={message.id}>{copy}{message.text}</p>;
  const command = message.source === "slash" || message.source === "shell";
  const when = sentAt(message.at);
  return <article ref={row} className={"message message-prompt " + message.source + (isActionsOpen ? " actions-open" : "")} data-message={message.id}
    onClick={() => { if (!window.getSelection()?.toString()) setActionsOpen((prior) => !prior); }}>
    {message.queued && <span className="message-queued">Queued</span>}
    <MessagePictures pictures={message.pictures} picture={picture} />
    {message.text && <div className="message-bubble">{command ? <code>{(message.source === "shell" ? "!" : "") + message.text}</code> : <MessageText text={message.text} />}</div>}
    {message.source === "answer" && <p className="message-origin">Answered on the board</p>}
    {message.source === "page" && <p className="message-origin">Answered on the page</p>}
    {(message.output || message.errors) && <pre className="message-output">{[message.output, message.errors].filter(Boolean).join("\n")}</pre>}
    {(copy || when.text) && <div className="message-actions" onClick={(event) => event.stopPropagation()}>
      {copy}
      {when.text && <time dateTime={message.at} data-tip={when.exact}>{when.text}</time>}
    </div>}
  </article>;
});
