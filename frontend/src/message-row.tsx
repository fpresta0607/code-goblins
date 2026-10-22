import { memo, useState } from "react";
import { copyText as copyToClipboard } from "./clipboard";
import { Disclosure } from "./Disclosure";
import { Icon } from "./Icon";
import { MessageCall } from "./message-call";
import { MessagePictures } from "./message-pictures";
import { MessageText } from "./message-text";
import { copyText, type PromptSource, type SessionMessage } from "./session-messages";
import type { Task, TreeNode } from "./types";

const COPIED_MS = 1400;
// Who a prompt is from, over its bubble: a Command Center answer is marked as
// one, and a notice has no bubble.
const FROM: Record<PromptSource, string> = { overlord: "You", cfo: "CFO", answer: "Command Center answer", notice: "", slash: "You", shell: "You", "": "You" };

// One message of a session as the Messages view draws it: a prompt in a
// bubble on the right under who it is from, a slash or ! command as typed
// with what it printed; a reply on the left, under the session's name where
// a run of replies begins, its text, its thinking folded and its calls; a
// compaction folded to its summary; an interrupt as Claude Code noted it.
// Each prompt, reply with text, notice and compaction has a Copy of its words.
export const MessageRow = memo(function MessageRow({ message, name, isFirstReply, task, picture, onChild }: {
  message: SessionMessage; name: string; isFirstReply: boolean; task?: Task; picture: (id: string) => string; onChild: (child: TreeNode, source: HTMLElement) => void;
}) {
  const [copied, setCopied] = useState(false);
  const words = copyText(message);
  const copy = words && <button type="button" className="icon-button message-copy" aria-label="Copy" data-tip={copied ? "Copied" : "Copy"}
    onClick={() => copyToClipboard(words).then(() => { setCopied(true); setTimeout(() => setCopied(false), COPIED_MS); }, () => {})}><Icon name={copied ? "check" : "copy"} /></button>;
  if (message.kind === "interrupt") return <p className="message-interrupt" data-message={message.id}>{message.text}</p>;
  if (message.kind === "compaction") return <div className="message-compaction" data-message={message.id}>
    {copy}
    <Disclosure kind="disclosure-line" title={message.trigger === "auto" ? "Compacted automatically" : "Compacted"}><MessageText text={message.text} /></Disclosure>
  </div>;
  if (message.kind === "reply") return <article className="message message-reply" data-message={message.id}>
    {isFirstReply && <header className="message-from"><Icon name="claude" />{name}</header>}
    {copy}
    {message.blocks.map((block, at) => block.kind === "text" ? <MessageText key={at} text={block.text} />
      : block.kind === "thinking" ? block.text && <Disclosure key={at} kind="disclosure-line message-thinking" title="Thinking"><MessageText text={block.text} /></Disclosure>
        : <MessageCall key={at} call={block} task={task} picture={picture} onChild={onChild} />)}
  </article>;
  if (message.source === "notice") return <p className="message-notice" data-message={message.id}>{copy}{message.text}</p>;
  const command = message.source === "slash" || message.source === "shell";
  return <article className={"message message-prompt " + message.source} data-message={message.id}>
    <header className="message-from">{copy}{FROM[message.source]}{message.queued && <span className="message-queued">Queued</span>}</header>
    <MessagePictures pictures={message.pictures} picture={picture} />
    {message.text && <div className="message-bubble">{command ? <code>{(message.source === "shell" ? "!" : "") + message.text}</code> : <MessageText text={message.text} />}</div>}
    {(message.output || message.errors) && <pre className="message-output">{[message.output, message.errors].filter(Boolean).join("\n")}</pre>}
  </article>;
});
