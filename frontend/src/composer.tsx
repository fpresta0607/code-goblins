import { useEffect, useRef, useState, type ChangeEvent, type ClipboardEvent, type KeyboardEvent } from "react";
import type { Snapshot, Task } from "./types";
import { message, reportToCfo, request, useResource } from "./api";
import { useChatStyle } from "./chat-style";
import { DictationButton } from "./dictation-button";
import { Icon } from "./Icon";
import { isPendingMessage, messagesTo, takesMessages } from "./messages";
import { parseCommands, slashOptions } from "./commands";
import { QueuedMessage } from "./queued-message";
import { RecentDictations } from "./recent-dictations";
import { useTerminalLink } from "./terminal-link";
import { readClipboard } from "./terminal-keys";
import { keepFile } from "./terminal-image";
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
// and Shift+Enter starts a new line. What keeps a message from the terminal
// says so beside the box, and the draft stays: a busy program, a view that is
// reconnecting or closed, typing a gate holds, and a screen of the harness's
// own open in its terminal, a question, a picker or a panel, which an amber
// card names with Answer in the terminal, since an Enter typed then would act
// on it. A goblin with no terminal that
// takes typing now, one paused, resuming or starting, gets what he sends as a
// message action, delivered once whatever it is doing, and the box lists his
// latest with where each is, and those a paused goblin's resume will carry,
// each of which can be deleted. Its microphone, held or tapped on a touch
// screen, and Ctrl+Shift+Space in the box dictate into it, and the board's
// recent dictations are a tap away.
//
// A picture pasted into this box, or a file added to it, is kept where the
// session he writes to reads it, and the message names its path, as the
// answer card's picture is kept for its asker (internal/supervisor/
// answer_images.go). It takes that road rather than the terminal views' own
// paste of a picture onto this PC's clipboard and the harness's image key,
// because this box also writes to a goblin that has no terminal up yet, and
// every harness reads a path; the clipboard road would need a live terminal
// and would leave a queued message without its picture. Alt+V and Ctrl+V both
// paste what the clipboard holds (the Overlord, 2026-10-26), Ctrl+C stays the
// harness's interrupt, and a paste the board cannot keep says so here.
export function Composer({ snapshot, task, name, terminal, focus = 0, onAnswer, onSent, hasConversation = false }: {
  snapshot: Snapshot; task?: Task; name: string; hasConversation?: boolean;
  // terminal is the terminal deck's key of the session's terminal, and
  // onSent is told of each message typed into it, so a screen of the
  // harness's own that it opens can bring the terminal in.
  terminal: string; focus?: number; onAnswer?: () => void; onSent?: (submission: string) => void;
}) {
  // A task whose terminal reads and takes messages directly is queued through
  // the board. So is a running task whose terminal refuses typing: its send
  // goes the same durable way rather than into a screen that will not take it.
  const queues = !!task && takesMessages(task);
  const link = useTerminalLink(terminal);
  const live = queues ? undefined : link;
  const draftKey = task?.id || terminal;
  const [draft, setDraft] = useState(() => storedDraft(draftKey));
  useEffect(() => storeDraft(draftKey, draft), [draftKey, draft]);
  const [isSending, setSending] = useState(false);
  // Why the last Send found no terminal to type into, or why a paste could
  // not be kept, until the draft changes.
  const [unsent, setUnsent] = useState("");
  const [isRecentOpen, setRecentOpen] = useState(false);
  const [isStyleOpen, setStyleOpen] = useState(false);
  // The session's own slash commands, read from its harness, and the command
  // the list has picked while he types its word.
  const commandQuery = new URLSearchParams(task ? { task: task.id, generation: task.generation } : { cfo: snapshot.cfo_terminal || "1", since: snapshot.cfo_terminal_since || "" });
  commandQuery.set("token", snapshot.instance);
  const commands = useResource("/api/commands?" + commandQuery.toString(), parseCommands);
  const [pick, setPick] = useState(0);
  const [isCommandsClosed, setCommandsClosed] = useState(false);
  const [adding, setAdding] = useState(0);
  const [, setEngine] = useState(0);
  // The kept message whose delete is on its way.
  const [deleting, setDeleting] = useState("");
  // One ID for what he is about to send to a goblin with no live terminal,
  // so a send retried after its answer was lost is the same message.
  const sendID = useRef("");
  const field = useRef<HTMLTextAreaElement>(null);
  const recent = useRef<HTMLButtonElement>(null);
  const files = useRef<HTMLInputElement>(null);
  const [style, chooseStyle] = useChatStyle();
  const closeRecent = () => { setRecentOpen(false); recent.current?.focus(); };
  useEffect(() => { if (focus) field.current?.focus(); }, [focus]);
  const write = (next: (prior: string) => string) => {
    setDraft(next);
    setUnsent("");
    setCommandsClosed(false);
    setPick(0);
    sendID.current = "";
  };
  const dictation = useDictation((text) => write((prior) => appendDictation(prior, text)), snapshot.instance);
  const post = (body: object) => request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify(body) });
  const held = live?.held ? heldNotice(live.held) : "";
  const isAsking = !!live?.screen?.isOpen;
  const isUnread = !!live?.isUnread;
  const unsentWhy = live?.phase === "closed" ? "Not sent. The terminal is closed." : live?.phase === "live" || live?.isReconnecting ? "Not sent. The terminal is reconnecting." : "Not sent. The terminal is connecting.";
  // What this box saves as a board action types nothing into the terminal, so
  // a harness picker or a busy program does not stop it: those guard direct
  // typing, which is the path they were written for.
  const savesThroughBoard = queues || !!held || isUnread;
  const send = async () => {
    const text = draft.trim();
    if (!text || isSending || adding || isAsking && !held) return;
    if (!savesThroughBoard) {
      // Direct typing waits for a readable program with no screen of its own
      // open, since an Enter typed then would act on that screen.
      if (isAsking || isUnread) return;
      const submission = live?.send(text)
      if (submission) { setDraft(""); setUnsent(""); onSent?.(submission); } else setUnsent(unsentWhy);
      return;
    }
    sendID.current ||= crypto.randomUUID();
    setSending(true);
    try {
      await post({ id: sendID.current, kind: "message", task_id: task?.id || "", generation: "", text });
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
  // A queued message is withdrawn by its own identity, so two messages with the
  // same words are told apart. A message kept for a resume has no action of its
  // own left, so it is named by its words in the note that carries it.
  const withdraw = async (text: string, key: string, target?: string) => {
    if (!task) return;
    setDeleting(key);
    try {
      await post({ id: crypto.randomUUID(), kind: "message_withdraw", task_id: task.id, generation: "", text, ...(target ? { target } : {}) });
    } catch (error: unknown) {
      reportToCfo("deleting a message to " + name, message(error));
    } finally {
      setDeleting("");
    }
  };
  // add keeps each file he pasted or added where the session reads it, and
  // names its path in the message. A file the board cannot keep says why in
  // the box rather than disappearing.
  const add = async (chosen: File[]) => {
    if (!chosen.length) return;
    setAdding((count) => count + chosen.length);
    for (const file of chosen) {
      try {
        const path = await keepFile(file, file.name || "pasted.png", snapshot.instance, task?.id);
        write((prior) => prior.trim() ? prior.replace(/\s+$/, "") + "\n" + path : path);
      } catch (error: unknown) {
        setUnsent("That " + (file.type.startsWith("image/") ? "picture" : "file") + " was not added: " + message(error));
      } finally {
        setAdding((count) => Math.max(0, count - 1));
      }
    }
  };
  // A picture pasted into the box arrives as a file the session can read. Text
  // keeps the browser's own paste, which the textarea takes as it always has.
  const paste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const pictures = Array.from(event.clipboardData?.files ?? []).filter((file) => file.type.startsWith("image/"));
    if (!pictures.length) return;
    event.preventDefault();
    void add(pictures);
  };
  const insert = (text: string) => {
    const box = field.current;
    const start = box?.selectionStart ?? draft.length, end = box?.selectionEnd ?? start;
    write((prior) => prior.slice(0, start) + text + prior.slice(end));
    requestAnimationFrame(() => { box?.setSelectionRange(start + text.length, start + text.length); box?.focus(); });
  };
  const pasteKey = async () => {
    const input = await readClipboard();
    if ("text" in input) insert(input.text);
    else if ("image" in input) await add([input.image]);
    else setUnsent(input.error);
  };
  const added = (event: ChangeEvent<HTMLInputElement>) => {
    const chosen = Array.from(event.target.files ?? []);
    event.target.value = "";
    void add(chosen);
  };
  const options = isCommandsClosed ? [] : slashOptions(commands.data ?? [], draft);
  const choose = (name: string) => {
    const gap = draft.search(/\s/);
    const args = gap >= 0 ? draft.slice(gap).replace(/\s+$/, "") : "";
    write(() => "/" + name + (args || " "));
    requestAnimationFrame(() => field.current?.focus());
  };
  const keys = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (dictation.key(event.nativeEvent) !== null) return;
    if (event.altKey && !event.ctrlKey && !event.metaKey && event.key.toLowerCase() === "v") {
      event.preventDefault();
      void pasteKey();
      return;
    }
    if (options.length) {
      if (event.key === "ArrowDown") { event.preventDefault(); setPick((prior) => (prior + 1) % options.length); return; }
      if (event.key === "ArrowUp") { event.preventDefault(); setPick((prior) => (prior - 1 + options.length) % options.length); return; }
      if (event.key === "Escape") { event.preventDefault(); setCommandsClosed(true); return; }
      if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
        event.preventDefault();
        choose(options[Math.min(pick, options.length - 1)]);
        return;
      }
    }
    if (event.key === "Tab" && event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey) {
      event.preventDefault();
      if (live?.control("\x1b[Z")) onAnswer?.();
      else setUnsent(held || unsentWhy);
      return;
    }
    if (event.key.toLowerCase() === "c" && event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey && event.currentTarget.selectionStart === event.currentTarget.selectionEnd) {
      event.preventDefault();
      if (!live?.control("\x03")) setUnsent(held || unsentWhy);
      return;
    }
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send(); }
  };
  const note = adding ? "Adding " + (adding > 1 ? adding + " files" : "the file") + "…" : held || unsent || (isUnread ? "Program busy. Typed keys are waiting." : "") || dictation.note;
  // What the board has kept for this goblin is what the board's own record of
  // it says, so it survives a reload and a remount, and it stays on screen
  // after a gate lets go so the action can be read and withdrawn.
  const sent = messagesTo(snapshot, task?.id || "");
  // A message the board still holds is in the conversation as its own entry;
  // the box keeps a quiet count of them and no second copy of their words.
  const queued = sent.filter(isPendingMessage);
  // Without a conversation, even a Modern panel needs the queue here.
  const queueRows = (!hasConversation ? sent : sent.filter((action) => action.status === "failed")).slice(-SHOWN);
  const kept = task?.lifecycle?.kept_messages ?? [];
  const revealQueued = () => document.querySelector<HTMLElement>("[data-queued]")?.scrollIntoView({ block: "center", behavior: "smooth" });
  const canSend = !isSending && !adding && !!draft.trim() && (!isAsking || !!held) && (savesThroughBoard || !isUnread);
  return <section className="composer" aria-label={"Messages to " + name}>
    {isAsking && <div className="composer-asking" role="status">
      <Icon name="warning" /><p>Waiting on you in the terminal</p>
      {onAnswer && <button type="button" className="labelled-button" onClick={onAnswer}><Icon name="terminal" />Answer in the terminal</button>}
    </div>}
    {queueRows.length + kept.length > 0 && <ol className="composer-sent">
      {queueRows.map((action) => <li key={action.id}><QueuedMessage action={action} instance={snapshot.instance} name={name} variant="queue" /></li>)}
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
    <div className="composer-box" onKeyDown={(event) => {
      if (event.key !== "Escape") return;
      if (isRecentOpen) { event.stopPropagation(); closeRecent(); }
      else if (isStyleOpen) { event.stopPropagation(); setStyleOpen(false); }
    }}>
      {options.length > 0 && <ul className="composer-commands" role="listbox" aria-label="Commands">
        {options.map((name, at) => <li key={name} role="option" aria-selected={at === Math.min(pick, options.length - 1)} onMouseDown={(event) => { event.preventDefault(); choose(name); }}><span>/{name}</span></li>)}
      </ul>}
      {isRecentOpen && <RecentDictations where="the message" model={dictation.model} onPaste={(text) => { write((prior) => appendDictation(prior, text)); setRecentOpen(false); field.current?.focus(); }}
        onClose={closeRecent} onEngine={() => setEngine((count) => count + 1)} />}
      <div className="composer-input">
        <button type="button" className="composer-control add-files" aria-label="Add files" data-tip="Add files" disabled={adding > 0} onClick={() => files.current?.click()}><Icon name="plus" /></button>
        <input ref={files} className="composer-file-input" type="file" multiple hidden aria-hidden="true" tabIndex={-1} onChange={added} />
        <textarea ref={field} aria-label={"Message " + name} placeholder={"Message " + name} rows={1} value={draft} disabled={isSending}
          onChange={(event) => { const value = event.target.value; write(() => value); }} onKeyDown={keys} onPaste={paste} onKeyUp={(event) => { dictation.key(event.nativeEvent); }} />
        <DictationButton dictation={dictation} />
      </div>
      <div className="composer-tools">
        <div className="composer-controls">
          <button type="button" className="composer-control" aria-haspopup="true" aria-expanded={isStyleOpen} aria-label={"Style: " + style} data-tip="How this conversation is drawn"
            onClick={() => setStyleOpen((prior) => !prior)}>{style === "classic" ? "Classic" : "Modern"}</button>
          {isStyleOpen && <div className="composer-style-popover" role="group" aria-label="Chat style">
            <button type="button" aria-pressed={style === "modern"} onClick={() => { chooseStyle("modern"); setStyleOpen(false); field.current?.focus(); }}>Modern</button>
            <button type="button" aria-pressed={style === "classic"} onClick={() => { chooseStyle("classic"); setStyleOpen(false); field.current?.focus(); }}>Classic</button>
          </div>}
          {hasConversation && queued.length > 0 && <button type="button" className="composer-control queued-control" aria-label={queued.length + (queued.length === 1 ? " queued message" : " queued messages")} data-tip="Show the queued messages in the conversation" onClick={revealQueued}><Icon name="clock" /><span>{queued.length}</span></button>}
        </div>
        <div className="composer-controls">
          <button ref={recent} type="button" className="composer-control" aria-label="Recent messages" aria-expanded={isRecentOpen} data-tip="Recent messages" onClick={() => setRecentOpen((prior) => !prior)}><Icon name="clock" /></button>
          {onAnswer && <button type="button" className="composer-control" aria-label="Terminal controls" data-tip="The session's controls, prompts and mode footer" onClick={onAnswer}><Icon name="terminal" /></button>}
          <button type="button" className="composer-send" aria-label={isSending ? "Sending" : "Send"} data-active={canSend || undefined} disabled={!canSend} onClick={() => void send()}>
            <Icon name={isSending ? "clock" : "send"} />
          </button>
        </div>
      </div>
      <p className="composer-note" role="status">{note}</p>
    </div>
  </section>;
}
