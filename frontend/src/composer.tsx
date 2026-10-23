import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import type { Snapshot, Task } from "./types";
import { message, reportToCfo, request } from "./api";
import { DictationButton } from "./dictation-button";
import { Icon } from "./Icon";
import { messagesTo, messageState, takesMessages } from "./messages";
import { RecentDictations } from "./recent-dictations";
import { useTerminalLink } from "./terminal-link";
import { heldNotice } from "./terminalInput";
import { appendDictation, useDictation } from "./useDictation";
import "./composer.css";

// How many of his latest messages to a goblin with no live terminal show
// above the box.
const SHOWN = 5;
// What he wrote and has not sent, kept per session for this tab's life, so a
// switch to another view or session, a reconnect and a reload of the board
// all find it where he left it.
const DRAFT_KEY = "cfo-draft:";

function storedDraft(key: string): string {
  try { return sessionStorage.getItem(DRAFT_KEY + key) ?? ""; } catch { return ""; }
}

function storeDraft(key: string, text: string): void {
  try {
    if (text) sessionStorage.setItem(DRAFT_KEY + key, text);
    else sessionStorage.removeItem(DRAFT_KEY + key);
  } catch { /* the draft stays while the box does */ }
}

// The message box under a session's messages, in place of its terminal's
// input. With a live terminal Send, or Enter, types what he wrote as one
// paste and presses Enter, through the terminal's own view (terminal-link.ts),
// and Shift+Enter starts a new line. While the harness works a working line
// offers Stop, which presses Escape. What keeps a message from the terminal
// says so beside the box, and the draft stays: a busy program, a view that is
// reconnecting or closed, typing a gate holds, and the harness asking at a
// screen of its own, which an amber card names with Answer in the terminal,
// since an Enter typed then would answer it. A goblin with no terminal that
// takes typing now, one paused, resuming or starting, gets what he sends as a
// message action, delivered once whatever it is doing, and the box lists his
// latest with where each is, and those a paused goblin's resume will carry,
// each of which can be deleted. Its microphone, held or tapped on a touch
// screen, and Ctrl+Shift+Space in the box dictate into it, and the board's
// recent dictations are a tap away.
export function Composer({ snapshot, task, name, terminal, isWorking = false, focus = 0, onAnswer }: {
  snapshot: Snapshot; task?: Task; name: string;
  // terminal is the terminal deck's key of the session's terminal.
  terminal: string; isWorking?: boolean; focus?: number; onAnswer?: () => void;
}) {
  const queues = !!task && takesMessages(task);
  const link = useTerminalLink(terminal);
  const live = queues ? undefined : link;
  const draftKey = task?.id || terminal;
  const [draft, setDraft] = useState(() => storedDraft(draftKey));
  useEffect(() => storeDraft(draftKey, draft), [draftKey, draft]);
  const [isSending, setSending] = useState(false);
  // Why the last Send found no terminal to type into, until the draft
  // changes.
  const [unsent, setUnsent] = useState("");
  const [isRecentOpen, setRecentOpen] = useState(false);
  const [, setEngine] = useState(0);
  // The kept message whose delete is on its way.
  const [deleting, setDeleting] = useState("");
  // One ID for what he is about to send to a goblin with no live terminal,
  // so a send retried after its answer was lost is the same message.
  const sendID = useRef("");
  const field = useRef<HTMLTextAreaElement>(null);
  const recent = useRef<HTMLButtonElement>(null);
  const closeRecent = () => { setRecentOpen(false); recent.current?.focus(); };
  useEffect(() => { if (focus) field.current?.focus(); }, [focus]);
  const write = (next: (prior: string) => string) => {
    setDraft(next);
    setUnsent("");
    sendID.current = "";
  };
  const dictation = useDictation((text) => write((prior) => appendDictation(prior, text)), snapshot.instance);
  const post = (body: object) => request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify(body) });
  const held = live?.held ? heldNotice(live.held) : "";
  const isAsking = !!live?.isAsking;
  const isUnread = !!live?.isUnread;
  const unsentWhy = live?.phase === "closed" ? "Not sent. The terminal is closed." : live?.phase === "live" || live?.isReconnecting ? "Not sent. The terminal is reconnecting." : "Not sent. The terminal is connecting.";
  const send = async () => {
    const text = draft.trim();
    if (!text || isSending || held || isAsking || isUnread) return;
    if (!task || !queues) {
      if (live?.send(text)) { setDraft(""); setUnsent(""); } else setUnsent(unsentWhy);
      return;
    }
    sendID.current ||= crypto.randomUUID();
    setSending(true);
    try {
      await post({ id: sendID.current, kind: "message", task_id: task.id, generation: "", text });
      sendID.current = "";
      storeDraft(draftKey, "");
      setDraft("");
    } catch (error: unknown) {
      reportToCfo("a message to " + name, message(error));
    } finally {
      setSending(false);
    }
  };
  // A delete names the message by its goblin and its words, which the
  // supervisor takes out of the resume note.
  const withdraw = async (text: string, key: string) => {
    if (!task) return;
    setDeleting(key);
    try {
      await post({ id: crypto.randomUUID(), kind: "message_withdraw", task_id: task.id, generation: "", text });
    } catch (error: unknown) {
      reportToCfo("deleting a message to " + name, message(error));
    } finally {
      setDeleting("");
    }
  };
  const keys = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (dictation.key(event.nativeEvent) !== null) return;
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send(); }
  };
  const note = held || unsent || (isUnread ? "Program busy. Typed keys are waiting." : "") || dictation.note;
  const sent = task && queues ? messagesTo(snapshot, task.id).slice(-SHOWN) : [];
  const kept = queues ? task?.lifecycle?.kept_messages ?? [] : [];
  return <section className="composer" aria-label={"Messages to " + name}>
    {isAsking && <div className="composer-asking" role="status">
      <Icon name="warning" /><p>Waiting on you in the terminal</p>
      {onAnswer && <button type="button" className="labelled-button" onClick={onAnswer}><Icon name="terminal" />Answer in the terminal</button>}
    </div>}
    {isWorking && live && !isAsking && <p className="composer-working" role="status"><span className="composer-spinner" aria-hidden="true" />Working
      <button type="button" className="labelled-button" onClick={() => live.escape()}><Icon name="pause" />Stop</button></p>}
    {sent.length + kept.length > 0 && <ol className="composer-sent">
      {sent.map((action) => <li key={action.id}><span className="composer-sent-text">{action.text}</span><span className="composer-sent-state">{messageState(action)}</span></li>)}
      {kept.map((text, index) => {
        const key = index + ":" + text;
        return <li key={key}>
          <span className="composer-sent-text">{text}</span>
          <span className="composer-sent-state">Kept for resume</span>
          <button type="button" className="icon-button" aria-label={"Delete: " + text} data-tip="Delete before its resume"
            disabled={deleting === key} onClick={() => void withdraw(text, key)}><Icon name="trash" /></button>
        </li>;
      })}
    </ol>}
    <div className="composer-box" onKeyDown={(event) => { if (isRecentOpen && event.key === "Escape") { event.stopPropagation(); closeRecent(); } }}>
      {isRecentOpen && <RecentDictations where="the message" model={dictation.model} onPaste={(text) => { write((prior) => appendDictation(prior, text)); setRecentOpen(false); field.current?.focus(); }}
        onClose={closeRecent} onEngine={() => setEngine((count) => count + 1)} />}
      <textarea ref={field} aria-label={"Message " + name} placeholder={"Message " + name} rows={2} value={draft} disabled={isSending}
        onChange={(event) => { const value = event.target.value; write(() => value); }} onKeyDown={keys} onKeyUp={(event) => { dictation.key(event.nativeEvent); }} />
      <div className="composer-tools">
        <DictationButton dictation={dictation} />
        <button ref={recent} type="button" className="icon-button" aria-label="Recent messages" aria-expanded={isRecentOpen} data-tip="Recent messages" onClick={() => setRecentOpen((prior) => !prior)}><Icon name="clock" /></button>
        <p className="composer-note" role="status">{note}</p>
        <button type="button" className="primary send-decision" disabled={isSending || !draft.trim() || !!held || isAsking || isUnread} onClick={() => void send()}>
          <Icon name={isSending ? "clock" : "send"} />{isSending ? "Sending" : "Send"}
        </button>
      </div>
    </div>
  </section>;
}
