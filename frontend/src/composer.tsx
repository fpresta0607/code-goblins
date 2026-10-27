import { useEffect, useRef, useState, type ChangeEvent, type ClipboardEvent, type KeyboardEvent } from "react";
import type { Snapshot, Task } from "./types";
import { message, reportToCfo, request, useResource } from "./api";
import { useChatStyle } from "./chat-style";
import { ContextUsage } from "./context-usage";
import { DictationButton } from "./dictation-button";
import { HarnessPanel } from "./harness-panel";
import { Icon } from "./Icon";
import { isPendingMessage, messagesTo, takesMessages } from "./messages";
import { mentionAt, mentionFile, parseFiles } from "./mentions";
import { parseCommands, slashOptions } from "./commands";
import { PANEL_KEYS, panelKey } from "./panel-keys";
import { PopupList } from "./popup-list";
import { QueuedMessage } from "./queued-message";
import { RecentDictations } from "./recent-dictations";
import type { SessionStats, UsageReading } from "./session-usage";
import { useTerminalLink } from "./terminal-link";
import { readClipboard } from "./terminal-keys";
import { keepFile } from "./terminal-image";
import { GATE_CUSTODY, isDetachedHold } from "./terminalInput";
import { appendDictation, useDictation } from "./useDictation";
import "./composer.css";

// How many of his latest messages to a goblin with no live terminal show
// above the box.
const SHOWN = 5;
// How long the @ list waits after a keystroke before it reads the files, so
// a burst of typing asks once.
const MENTION_WAIT_MS = 120;
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
// says so once beside the box: a busy program, a view that is reconnecting and
// typing a gate holds save it through the durable queue; a view that is closed
// or connecting keeps the draft. A screen of the harness's own open in its
// terminal, a question, a permission prompt, a picker or a panel, is drawn
// above the box as a list he picks from (harness-panel.tsx), and while it is
// open the box answers it with the terminal's own keys: an empty box hands it
// the arrows, Enter, Escape, Tab, Space and a choice's number, and Send types
// what he wrote into it without pressing Enter, which the list's Enter does;
// while typing is held the message queues instead. A goblin with no terminal
// that takes typing now, one paused, resuming or starting, gets what he sends
// as a message action, delivered once whatever it is doing, and the box lists
// his latest with where each is, and those a paused goblin's resume will
// carry, each of which can be deleted. Its microphone, held or tapped on a
// touch screen, and Ctrl+Shift+Space in the box dictate into it, and the
// board's recent dictations are a tap away.
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
export function Composer({ snapshot, task, name, terminal, focus = 0, onShowTerminal, onOpenTerminal, terminalFeedback, stats, reading, model, hasConversation = false, isTerminalShown = false, isShown = true, onActive }: {
  snapshot: Snapshot; task?: Task; name: string; hasConversation?: boolean;
  stats?: SessionStats; reading?: UsageReading; model?: string; onOpenTerminal?: () => void; terminalFeedback?: string;
  // terminal is the terminal deck's key of the session's terminal, and
  // onShowTerminal steps that terminal in, for a screen of the harness's own
  // its list does not cover.
  terminal: string; focus?: number; onShowTerminal?: () => void;
  isTerminalShown?: boolean; isShown?: boolean; onActive?: (terminal: string, isActive: boolean) => void;
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
  // The session's slash commands, read from its harness, the files its @
  // names, and the option the open list has picked while he types its word.
  // Escape closes the list until the draft changes. A box that is not on
  // screen reads nothing: its own commands are read when he can see and use
  // it, so a hidden box never reports a refusal.
  const sessionQuery = new URLSearchParams(task ? { task: task.id, generation: task.generation } : { cfo: snapshot.cfo_terminal || "1", since: snapshot.cfo_terminal_since || "" });
  sessionQuery.set("token", snapshot.instance);
  const commands = useResource(isShown ? "/api/commands?" + sessionQuery.toString() : null, parseCommands);
  const [pick, setPick] = useState(0);
  const [isListClosed, setListClosed] = useState(false);
  const [caret, setCaret] = useState(0);
  const [found, setFound] = useState<string[]>([]);
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
  // The box is one mounted composer moved to the surface on screen, so a
  // switch asks for its keyboard a frame or two before the move lands and the
  // browser cannot focus a node that is not in the page yet. Ask again, a few
  // frames at most, until it is.
  useEffect(() => {
    if (!focus) return;
    let frames = 0, next = 0;
    const ask = () => {
      const box = field.current;
      if (box?.isConnected && box.getClientRects().length) { box.focus(); if (box.matches(":focus")) return; }
      if (frames++ < 8) next = requestAnimationFrame(ask);
    };
    ask();
    return () => cancelAnimationFrame(next);
  }, [focus]);
  const write = (next: (prior: string) => string) => {
    setDraft(next);
    setUnsent("");
    setListClosed(false);
    setPick(0);
    sendID.current = "";
  };
  const dictation = useDictation((text) => write((prior) => appendDictation(prior, text)), snapshot.instance);
  const isActive = !!(adding || isSending || unsent || dictation.listening);
  useEffect(() => {
    onActive?.(terminal, isActive);
    return () => onActive?.(terminal, false);
  }, [terminal, isActive, onActive]);
  const post = (body: object) => request("/api/actions", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify(body) });
  const isHeld = !!live?.held;
  const isAsking = !!live?.screen?.isOpen;
  // A screen the relay could not read as a list still offers its keys.
  const panel = isAsking ? live?.screen?.panel ?? { rows: [], choices: [], hints: [] } : null;
  // The permission mode the session's terminal shows, which Shift+Tab cycles.
  const mode = live?.screen?.mode ?? "";
  const isUnread = !!live?.isUnread;
  const harness = task?.harness || snapshot.cfo_harness;
  const isEngineSupported = ["claude", "codex", "pi"].includes(harness);
  const canChangeEngine = isEngineSupported && !queues && link?.phase === "live" && !link.isReconnecting && !!link.screen && !link.screen.isOpen && !link.isUnread && !link.held;
  const changeEngine = (setting: "model" | "effort") => {
    if (canChangeEngine) link.send(setting === "model" || harness === "codex" ? "/model" : harness === "claude" ? "/effort" : "/thinking");
  };
  const unsentWhy = live?.phase === "closed" ? "Not sent. The terminal is closed." : live?.phase === "live" || live?.isReconnecting ? "Not sent. The terminal is reconnecting." : "Not sent. The terminal is connecting.";
  // What this box saves as a board action types nothing into the terminal, so
  // a harness picker or a busy program does not stop it: those guard direct
  // typing, which is the path they were written for.
  const savesThroughBoard = queues || isHeld || isUnread || !!live?.isReconnecting;
  const send = async () => {
    const text = draft.trim();
    if (!text || isSending || adding) return;
    if (!savesThroughBoard && !sendID.current) {
      // A screen of the harness's own takes what he wrote as typing, on one
      // line and with no Enter, which would act on the screen before he
      // chose: the list's Enter confirms. Otherwise direct typing waits for a
      // readable program.
      if (isAsking) {
        if (live?.control(text.replace(/\s*\n\s*/g, " "))) { setDraft(""); setUnsent(""); } else setUnsent(unsentWhy);
        return;
      }
      if (isUnread) return;
      if (live?.send(text)) { setDraft(""); setUnsent(""); } else setUnsent(unsentWhy);
      return;
    }
    sendID.current ||= crypto.randomUUID();
    setSending(true);
    try {
      await post({ id: sendID.current, kind: "message", task_id: task?.id || "", generation: "", text });
      sendID.current = "";
      storeDraft(draftKey, "");
      setDraft("");
      setUnsent("");
    } catch (error: unknown) {
      setUnsent("Not sent. " + message(error));
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
  // insert puts words where the caret or the selection is, keeping the rest of
  // the draft, and separates them from a word they would otherwise run into.
  const insert = (text: string) => {
    const box = field.current;
    const prior = box?.value ?? draft;
    const heard = text.trim();
    const start = box?.selectionStart ?? prior.length, end = box?.selectionEnd ?? start;
    const before = prior.slice(0, start), after = prior.slice(end);
    const lead = heard && before && !/\s$/.test(before) ? " " : "";
    const tail = heard && after && !/^\s/.test(after) ? " " : "";
    write(() => before + lead + heard + tail + after);
    const at = start + lead.length + heard.length;
    requestAnimationFrame(() => { box?.setSelectionRange(at, at); box?.focus(); });
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
  // The open list: the slash commands while he types the command word, or
  // the session's files while the caret stands in an @, read a moment after
  // he stops typing, the last list kept on screen meanwhile.
  const commandOptions = isListClosed ? [] : slashOptions(commands.data ?? [], draft);
  const mention = isListClosed || commandOptions.length ? null : mentionAt(draft, caret);
  const mentionQuery = mention?.query ?? null;
  const filesAddress = "/api/files?" + sessionQuery.toString();
  useEffect(() => {
    if (mentionQuery === null) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      request(filesAddress + "&q=" + encodeURIComponent(mentionQuery), controller.signal).then(parseFiles)
        .then(setFound, (error: unknown) => { if (!controller.signal.aborted) reportToCfo("the files of " + name, message(error)); });
    }, MENTION_WAIT_MS);
    return () => { clearTimeout(timer); controller.abort(); };
  }, [mentionQuery, filesAddress, name]);
  const mentionFiles = mention ? found : [];
  const listOptions = commandOptions.length
    ? commandOptions.map((command) => ({ key: command.name, label: "/" + command.name, detail: command.description }))
    : mentionFiles.map((file) => ({ key: file, label: file.slice(file.lastIndexOf("/") + 1), detail: file }));
  const picked = Math.min(pick, listOptions.length - 1);
  const choose = (at: number) => {
    const box = field.current;
    if (commandOptions.length) {
      const gap = draft.search(/\s/);
      const args = gap >= 0 ? draft.slice(gap).replace(/\s+$/, "") : "";
      write(() => "/" + commandOptions[at].name + (args || " "));
      requestAnimationFrame(() => box?.focus());
      return;
    }
    if (!mention) return;
    const chosen = mentionFile(draft, mention, caret, mentionFiles[at]);
    write(() => chosen.text);
    setCaret(chosen.caret);
    requestAnimationFrame(() => { box?.focus(); box?.setSelectionRange(chosen.caret, chosen.caret); });
  };
  const keys = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (dictation.key(event.nativeEvent) !== null) return;
    if (event.altKey && !event.ctrlKey && !event.metaKey && event.key.toLowerCase() === "v") {
      event.preventDefault();
      void pasteKey();
      return;
    }
    if (listOptions.length) {
      if (event.key === "ArrowDown") { event.preventDefault(); setPick((prior) => (Math.min(prior, listOptions.length - 1) + 1) % listOptions.length); return; }
      if (event.key === "ArrowUp") { event.preventDefault(); setPick((prior) => (Math.min(prior, listOptions.length - 1) - 1 + listOptions.length) % listOptions.length); return; }
      if (event.key === "Escape") { event.preventDefault(); setListClosed(true); return; }
      if ((event.key === "Enter" || event.key === "Tab") && !event.shiftKey && !event.nativeEvent.isComposing) {
        event.preventDefault();
        choose(picked);
        return;
      }
    }
    // An open screen of the harness's own takes the terminal's keys from an
    // empty box, so it is answered here as in the terminal.
    const keysForPanel = panel && !isHeld && !draft ? panelKey(event, panel) : null;
    if (keysForPanel !== null) {
      event.preventDefault();
      if (!live?.control(keysForPanel)) setUnsent(unsentWhy);
      return;
    }
    if (event.key === "Tab" && event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey) {
      event.preventDefault();
      if (!live?.control("\x1b[Z")) setUnsent(isHeld ? "Typing is paused." : unsentWhy);
      return;
    }
    if (event.key.toLowerCase() === "c" && event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey && event.currentTarget.selectionStart === event.currentTarget.selectionEnd) {
      event.preventDefault();
      if (!live?.control("\x03")) setUnsent(isHeld ? "Typing is paused." : unsentWhy);
      return;
    }
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send(); }
  };
  const queueNote = live?.held && isDetachedHold(live.held) ? "Detached worktree. Send to queue until a branch is ready."
    : live?.held && GATE_CUSTODY.test(live.held) ? "Review in progress. Send to queue until it finishes."
      : savesThroughBoard ? "Messages will be queued." : "";
  const note = unsent || terminalFeedback || (adding ? "Adding " + (adding > 1 ? adding + " files" : "the file") + "…" : live?.feedback || dictation.note || queueNote);
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
  const canSend = !isSending && !adding && !!draft.trim() && (savesThroughBoard || isAsking || !isUnread);
  return <section className="composer" aria-label={"Messages to " + name}>
    {panel && !isHeld && !isTerminalShown && <HarnessPanel panel={panel} name={name} onShowTerminal={onShowTerminal}
      onKeys={(keys) => { if (!live?.control(keys)) setUnsent(unsentWhy); }} />}
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
      {listOptions.length > 0 && <PopupList label={commandOptions.length ? "Commands" : "Files"} options={listOptions} pick={picked} onChoose={choose} />}
      {isRecentOpen && <RecentDictations where="the message" model={dictation.model} onPaste={(text) => { insert(text); setRecentOpen(false); }}
        onClose={closeRecent} onEngine={() => setEngine((count) => count + 1)} />}
      <div className="composer-input">
        <button type="button" className="composer-control add-files" aria-label="Add files" data-tip="Add files" disabled={adding > 0} onClick={() => files.current?.click()}><Icon name="plus" /></button>
        <input ref={files} className="composer-file-input" type="file" multiple hidden aria-hidden="true" tabIndex={-1} onChange={added} />
        <textarea ref={field} aria-label={"Message " + name} placeholder={"Message " + name} rows={1} value={draft} disabled={isSending}
          onChange={(event) => { const value = event.target.value; setCaret(event.target.selectionStart); write(() => value); }} onSelect={(event) => setCaret(event.currentTarget.selectionStart)} onKeyDown={keys} onPaste={paste} onKeyUp={(event) => { dictation.key(event.nativeEvent); }} />
        {live?.isDictating ? <button type="button" className="voice-bubble recording" aria-label="Stop terminal dictation" onClick={live.stopDictation}><Icon name="mic" /></button> : <DictationButton dictation={dictation} />}
      </div>
      <div className="composer-tools">
        <div className="composer-controls">
          <button type="button" className="composer-control" aria-haspopup="true" aria-expanded={isStyleOpen} aria-label={"Style: " + style} data-tip="How this conversation is drawn"
            onClick={() => setStyleOpen((prior) => !prior)}>{style === "classic" ? "Classic" : "Modern"}</button>
          {isStyleOpen && <div className="composer-style-popover" role="group" aria-label="Chat style">
            <button type="button" aria-pressed={style === "modern"} onClick={() => { chooseStyle("modern"); setStyleOpen(false); field.current?.focus(); }}>Modern</button>
            <button type="button" aria-pressed={style === "classic"} onClick={() => { chooseStyle("classic"); setStyleOpen(false); field.current?.focus(); }}>Classic</button>
          </div>}
          {mode && <button type="button" className="composer-control mode-chip" aria-label={"Mode: " + mode + ". Shift+Tab changes it"} data-tip="Shift+Tab changes the mode" disabled={isHeld}
            onClick={() => { if (!live?.control(PANEL_KEYS.backTab)) setUnsent(unsentWhy); }}><Icon name="shield" />{mode[0].toUpperCase() + mode.slice(1)}</button>}
          {hasConversation && queued.length > 0 && <button type="button" className="composer-control queued-control" aria-label={queued.length + (queued.length === 1 ? " queued message" : " queued messages")} data-tip="Show the queued messages in the conversation" onClick={revealQueued}><Icon name="clock" /><span>{queued.length}</span></button>}
        </div>
        <div className="composer-controls">
          {!queues && <>
            <button type="button" className="composer-control engine-control" aria-label="Change model" disabled={!canChangeEngine} data-tip={canChangeEngine ? "Choose in the session's model picker" : "Model controls need a connected, unheld composer"} onClick={() => changeEngine("model")}>{model || task?.model || "Model"}</button>
            <button type="button" className="composer-control engine-control" aria-label="Change effort" disabled={!canChangeEngine} data-tip={canChangeEngine ? "Choose the session's supported reasoning effort" : "Effort controls need a connected, unheld composer"} onClick={() => changeEngine("effort")}>Effort</button>
          </>}
          <button ref={recent} type="button" className="composer-control" aria-label="Speech history" aria-expanded={isRecentOpen} data-tip="Speech history" onClick={() => setRecentOpen((prior) => !prior)}><Icon name="clock" /></button>
          {onOpenTerminal && <button type="button" className="composer-control" aria-label="Open in Windows Terminal" data-tip="Open this session in its own terminal window" onClick={onOpenTerminal}><Icon name="terminal" /></button>}
          {stats && reading && <ContextUsage stats={stats} reading={reading} />}
          <button type="button" className="composer-send" aria-label={isSending ? "Sending" : "Send"} data-active={canSend || undefined} disabled={!canSend} onClick={() => void send()}>
            <Icon name={isSending ? "clock" : "send"} />
          </button>
        </div>
      </div>
      <p className="composer-note" role={unsent ? "alert" : "status"}>{note}</p>
    </div>
  </section>;
}
