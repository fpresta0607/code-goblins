import { memo, useEffect, useRef, useState } from "react";
import { copyText as copyToClipboard } from "./clipboard";
import { Disclosure } from "./Disclosure";
import { HarnessMark } from "./harness-mark";
import { Icon } from "./Icon";
import { keptPictures } from "./kept-files";
import { LongText } from "./long-text";
import { MessageCall } from "./message-call";
import { MessagePictures } from "./message-pictures";
import { MessageText } from "./message-text";
import { inlineElements, isLongText } from "./messageText";
import { copyText, replyParts, type SessionMessage } from "./session-messages";
import type { Task, TreeNode } from "./types";

const COPIED_MS = 1400;

// When a message was sent, short: its date and time of day.
function sentAt(at: string): string {
  const time = at && !at.startsWith("0001") ? new Date(at) : null;
  if (!time || Number.isNaN(time.getTime())) return "";
  return time.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

// readAt says when the session read his message: the time its own record
// shows it at (the Overlord, 2026-10-29: "the label becomes Read 6:45 AM").
function readAt(at: string): string {
  const time = at && !at.startsWith("0001") ? new Date(at) : null;
  return time && !Number.isNaN(time.getTime()) ? "Read " + time.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" }) : "Read";
}

// One message of a session as the Messages view draws it: a prompt in a
// quiet bubble on the right, a slash or ! command as typed with what it
// printed; a reply as plain prose with its thinking folded to one line that
// opens, and each run of its calls as one quiet stack of rows; a compaction
// folded to its summary; an interrupt as a plain Interrupted row. What says who spoke is the side and the bubble, never a
// speaker line (the Overlord, 2026-10-26).
// A long message of his, and a long print of a command, shows its first
// lines with Show more (LongText), so a paste never fills a phone's screen.
// Under each of his own messages nothing shows until he taps the message or
// points at it: then when he sent it and Copy show in that spot, which closes
// when he taps away (the Overlord, 2026-10-28: "Only when you hover in that
// position where it says 'Sent just now', that's where the timestamp and copy
// icon come"). Its words stay selectable. The pictures he sent show above
// his bubble, on his side, at a size he can read, as Claude's app draws them
// (the Overlord, 2026-10-28: "see how claude includes pictures when pasted"):
// those the transcript holds, read back by id (picture), and those the
// message box kept and named by path, which show in place of the path
// (keptPicture), never the path itself. A reply shows when it arrived and
// Copy only where it ends its turn (isTurnEnd), once the turn has ended, its
// time, then Copy, then the mark of the session's harness (the Overlord,
// 2026-10-28: "Copy should come after the timestamp. The icon should come
// after the timestamp", and "why the stray copy button in our chat?"): a
// short text between tool calls is not a finished message.
// openFolds are the folds of this message he opened, kept by the view so
// they stay open when the row leaves the window and comes back, and onFold is
// told of each one he opens or closes.
export const MessageRow = memo(function MessageRow({ message, task, harness, picture, keptPicture, onChild, openFolds, onFold, isTurnEnd }: {
  message: SessionMessage; task?: Task; harness: string; picture: (id: string) => string; keptPicture: (path: string) => string; onChild: (child: TreeNode, source: HTMLElement) => void;
  openFolds: ReadonlySet<string>; onFold: (message: string, fold: string, isOpen: boolean) => void; isTurnEnd: boolean;
}) {
  const fold = (key: string) => ({ open: openFolds.has(key), onOpenChange: (isOpen: boolean) => onFold(message.id, key, isOpen) });
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
  const when = sentAt(message.at);
  const copy = words && <button type="button" className="icon-button message-copy" aria-label="Copy"
    onClick={(event) => { event.stopPropagation(); copyToClipboard(words).then(() => { setCopied(true); setTimeout(() => setCopied(false), COPIED_MS); }, () => {}); }}><Icon name={copied ? "check" : "copy"} /></button>;
  if (message.kind === "interrupt") return <p className="message-interrupt" data-message={message.id}><Icon name="pause" />Interrupted</p>;
  if (message.kind === "compaction") return <div className="message-compaction" data-message={message.id}>
    {copy}
    <Disclosure kind="disclosure-line" title={message.trigger === "auto" ? "Compacted automatically" : "Compacted"} {...fold("compaction")}><MessageText text={message.text} /></Disclosure>
  </div>;
  if (message.kind === "reply") return <article className="message message-reply" data-message={message.id}>
    {replyParts(message.blocks).map((part) => part.kind === "calls"
      ? <div key={part.at} className="message-steps">{part.calls.map((call, at) => <MessageCall key={at} call={call} task={task} picture={picture} onChild={onChild} {...fold("call:" + part.at + ":" + at)} />)}</div>
      : part.kind === "text" ? <MessageText key={part.at} text={part.text} />
        : <Disclosure key={part.at} {...fold("thinking:" + part.at)} kind="disclosure-line message-thinking" title={<><span className="thinking-label">Thinking</span><span className="thinking-preview">{inlineElements(part.text.trim().split("\n")[0])}</span></>}>
          <MessageText text={part.text} />
        </Disclosure>)}
    {isTurnEnd && (copy || when) && <div className="message-reply-actions">
      {when && <time dateTime={message.at}>{when}</time>}
      {copy}
      {harness && <HarnessMark harness={harness} />}
    </div>}
  </article>;
  if (message.source === "notice") return <p className="message-notice" data-message={message.id}>{copy}{message.text}</p>;
  const command = message.source === "slash" || message.source === "shell";
  const printed = [message.output, message.errors].filter(Boolean).join("\n");
  const kept = command ? { text: message.text, paths: [] } : keptPictures(message.text);
  const pictures = [...message.pictures.map((each) => picture(each.id)), ...kept.paths.map(keptPicture)];
  return <article ref={row} className={"message message-prompt " + message.source + (isActionsOpen ? " actions-open" : "")} data-message={message.id}
    onClick={() => { if (!window.getSelection()?.toString()) setActionsOpen((prior) => !prior); }}>
    <MessagePictures sources={pictures} />
    {kept.text && <div className="message-bubble"><LongText isLong={isLongText(kept.text)} {...fold("more")}>
      {command ? <code>{(message.source === "shell" ? "!" : "") + kept.text}</code> : <MessageText text={kept.text} />}
    </LongText></div>}
    {(message.source === "overlord" || command) && <p className="message-state">{readAt(message.at)}</p>}
    {message.source === "answer" && <p className="message-origin">Answered on the board</p>}
    {message.source === "page" && <p className="message-origin">Answered on the page</p>}
    {printed && <LongText isLong={isLongText(printed)} {...fold("output")}><pre className="message-output">{printed}</pre></LongText>}
    {(copy || when) && <div className="message-actions" onClick={(event) => event.stopPropagation()}>
      {when && <time dateTime={message.at}>{when}</time>}
      {copy}
    </div>}
  </article>;
});
