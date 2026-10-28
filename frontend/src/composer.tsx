import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ChangeEvent, type ClipboardEvent, type KeyboardEvent } from "react";
import type { Snapshot, Task } from "./types";
import { message, reportToCfo, request, useResource } from "./api";
import { useChatStyle } from "./chat-style";
import { DictationButton } from "./dictation-button";
import { parseEngineCatalog, type EngineModel } from "./engine-catalog";
import { CODEX_NOT_SENT, driveCodexPicker } from "./codex-picker";
import { effortTone, engineCommand, settingCommand, type EngineSetting } from "./engine-choices";
import { EnginePane } from "./engine-pane";
import { HarnessMark } from "./harness-mark";
import { HarnessPanel } from "./harness-panel";
import { Icon } from "./Icon";
import { isPendingMessage, messagesTo, takesMessages } from "./messages";
import { mentionAt, mentionedFiles, mentionFile, parseFiles } from "./mentions";
import { shortModel } from "./model-name";
import { Notice } from "./notice";
import { parseCommands, slashOptions } from "./commands";
import { addPendingSend, removePendingSend, usePendingSends } from "./pending-sends";
import { PANEL_KEYS, panelKey } from "./panel-keys";
import { PopupList } from "./popup-list";
import { QueuedMessage } from "./queued-message";
import { RecentDictations } from "./recent-dictations";
import { useTerminalLink } from "./terminal-link";
import { readClipboard } from "./terminal-keys";
import { keepFile } from "./terminal-image";
import { GATE_CUSTODY, isDetachedHold } from "./terminalInput";
import { appendDictation, useDictation } from "./useDictation";
import "./composer.css";

// The permission modes the box names in the fewest words, as the terminal's
// footer names them in more.
const MODE_NAMES: Record<string, string> = { "auto mode": "Auto", "plan mode": "Plan", "accept edits": "Accept edits", "bypass permissions": "Bypass", "don't ask": "Don't ask", "manual mode": "Manual" };

// How many of his latest messages to a goblin with no live terminal show
// above the box.
const SHOWN = 5;
// How long the @ list waits after a keystroke before it reads the files, so
// a burst of typing asks once.
const MENTION_WAIT_MS = 120;
// How long the box waits for Codex's /model picker to show the choice it
// set out to pick before it closes the picker and says so.
const CODEX_PICKER_WAIT_MS = 8000;
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
// terminal that lists choices, a question, a permission prompt or a picker,
// or one the relay could not read, is drawn above the box as a list he picks
// from (harness-panel.tsx), and while it is open the box answers it with the
// terminal's own keys: an empty box hands it the arrows, Enter, Escape, Tab,
// Space and a choice's number, and Send pastes what he wrote into a list of
// choices on one line without pressing Enter, which the list's Enter does;
// while typing is held the message queues instead. At any other open screen,
// and in Modern Chat while the harness is in a turn, Send holds the message
// through the board (holdsForTurn). A goblin with
// no terminal that takes typing now, one paused, resuming or starting, gets
// what he sends as a message action, delivered once whatever it is doing, and
// the box lists his latest with where each is, and those a paused goblin's
// resume will carry, each of which can be deleted. Its microphone, held or
// tapped on a touch screen, and Ctrl+Shift+Space in the box dictate into it,
// and the board's recent dictations are a tap away.
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
export function Composer({ snapshot, task, name, terminal, focus = 0, onShowTerminal, onOpenTerminal, terminalFeedback, model, effort, hasConversation = false, isTerminalShown = false, isShown = true, isWorking = false, history = [], onActive }: {
  snapshot: Snapshot; task?: Task; name: string; hasConversation?: boolean;
  model?: string; onOpenTerminal?: () => void; terminalFeedback?: string;
  // model and effort are what the session's own record last says it runs.
  effort?: string;
  // terminal is the terminal deck's key of the session's terminal, and
  // onShowTerminal steps that terminal in, for a screen of the harness's own
  // its list does not cover.
  terminal: string; focus?: number; onShowTerminal?: () => void;
  isTerminalShown?: boolean; isShown?: boolean; onActive?: (terminal: string, isActive: boolean) => void;
  // isWorking says the session's harness is running a turn, which Stop in
  // the send button's place interrupts while the box is empty, and history
  // is what he sent the session before, oldest first, which Up recalls.
  isWorking?: boolean; history?: string[];
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
  // Which of the board's own setting panes is open over the row, if one is.
  const [pane, setPane] = useState<EngineSetting | null>(null);
  // What he last picked in a pane, kept on its control until the session's
  // record says something other than what it said when he picked (over).
  const [chosenEngine, setChosenEngine] = useState<{ model?: { value: string; over: string }; effort?: { value: string; over: string } }>({});
  // Whether the box is answering Codex's own /model picker, which takes no
  // value (codex-picker.ts), and what stops that; the harness's list is not
  // drawn meanwhile.
  const [isDriving, setDriving] = useState(false);
  const stopDriving = useRef<(() => void) | null>(null);
  useEffect(() => () => stopDriving.current?.(), []);
  // Which of his earlier messages Up has brought back into the box, if any.
  const [recalled, setRecalled] = useState<number | null>(null);
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
  const [found, setFound] = useState<{ start: number | null; query: string; files: string[] }>({ start: null, query: "", files: [] });
  const [adding, setAdding] = useState(0);
  const [, setEngine] = useState(0);
  // The kept message whose delete is on its way.
  const [deleting, setDeleting] = useState("");
  // One ID for what he is about to send to a goblin with no live terminal,
  // so a send retried after its answer was lost is the same message.
  const sendID = useRef("");
  const field = useRef<HTMLTextAreaElement>(null);
  // A press outside a pane closes it and leaves the keyboard where he pressed;
  // Escape and a pick hand it back to the box.
  const closePane = useCallback(() => setPane(null), []);
  const modelControl = useRef<HTMLButtonElement>(null);
  const effortControl = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLButtonElement>(null);
  const composerBox = useRef<HTMLDivElement>(null);
  const files = useRef<HTMLInputElement>(null);
  const [style, chooseStyle] = useChatStyle();
  const closeRecent = () => { setRecentOpen(false); menu.current?.focus(); };
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
  // While he writes at the end of a draft taller than the box's cap, the
  // box keeps its last lines and the caret in view, as dictated words or a
  // paste land there too.
  useLayoutEffect(() => {
    const box = field.current;
    if (box && document.activeElement === box && box.selectionEnd === box.value.length) box.scrollTop = box.scrollHeight;
  }, [draft]);
  const write = (next: (prior: string) => string) => {
    setDraft(next);
    setRecalled(null);
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
  // A real question of the harness's own, a list to choose from, is drawn
  // and answered here, and a screen the relay could not read offers its way
  // out; a screen of words with nothing to choose, such as the prompt line
  // with his message on it or the harness's words for a usage limit, is
  // never mirrored (the Overlord, 2026-10-28: "What is that window above the
  // chat input ... remove it"). Only a real question takes what he writes as
  // its answer.
  const isScreenOpen = !!live?.screen?.isOpen;
  const screenPanel = isScreenOpen ? live?.screen?.panel ?? null : null;
  const panel = screenPanel && (screenPanel.choices.length > 0 || screenPanel.rows.length === 0) ? screenPanel : null;
  const isAsking = !!panel && panel.choices.length > 0;
  // The permission mode the session's terminal shows, which Shift+Tab cycles.
  const mode = live?.screen?.mode ?? "";
  const isUnread = !!live?.isUnread;
  const harness = task?.harness || snapshot.cfo_harness;
  const isEngineSupported = ["claude", "codex", "pi"].includes(harness);
  const canChangeEngine = isEngineSupported && !queues && link?.phase === "live" && !link.isReconnecting && !!link.screen && !link.screen.isOpen && !link.isUnread && !link.held;
  const unsentWhy = live?.phase === "closed" ? "Not sent. The terminal is closed." : live?.phase === "live" || live?.isReconnecting ? "Not sent. The terminal is reconnecting." : "Not sent. The terminal is connecting.";
  const recordedModel = model || task?.model || "";
  const recordedEffort = effort || task?.effort || "";
  // A pick shows until the session's record says something new, and is then
  // dropped, so a record that later returns to what it said before never
  // brings the old pick back.
  if (chosenEngine.model && chosenEngine.model.over !== recordedModel || chosenEngine.effort && chosenEngine.effort.over !== recordedEffort) {
    setChosenEngine((prior) => ({ model: prior.model?.over === recordedModel ? prior.model : undefined, effort: prior.effort?.over === recordedEffort ? prior.effort : undefined }));
  }
  const shownModel = chosenEngine.model && chosenEngine.model.over === recordedModel ? chosenEngine.model.value : recordedModel;
  const shownEffort = chosenEngine.effort && chosenEngine.effort.over === recordedEffort ? chosenEngine.effort.value : recordedEffort;
  // The model by its short, proper name (model-name.ts), never its raw id (the
  // Overlord, 2026-10-28: "claude-opus-5-5 is not proper case").
  const modelName = shortModel(shownModel);
  // applyEngine sets the session's model or effort in its harness, as Classic
  // would through the harness's own menu (the Overlord, 2026-10-27: "they
  // should control the same interfaces. Same session"): one line for Claude
  // Code and pi, and Codex's own picker, answered here, for Codex. A pick from
  // the pane, which lists only what the harness offers, shows on its control
  // at once (shouldShow); a value typed after the command shows once the
  // session's record says it runs it.
  const applyEngine = (setting: EngineSetting, value: string, shouldShow: boolean, chosen?: EngineModel) => {
    setPane(null);
    if (!canChangeEngine || !value) return;
    const command = engineCommand(harness, setting, value);
    const nextEffort = setting === "effort" ? value : chosen?.efforts.includes(shownEffort) ? shownEffort : chosen?.default_effort ?? "";
    if (command ? !link.send(command) : !link.send("/model")) { setUnsent(unsentWhy); return; }
    if (shouldShow) setChosenEngine((prior) => setting === "model" ? { ...prior, model: { value, over: recordedModel } } : { ...prior, effort: { value, over: recordedEffort } });
    if (!command) {
      stopDriving.current?.();
      setDriving(true);
      stopDriving.current = driveCodexPicker(terminal, { model: setting === "model" ? value : shownModel, name: setting === "model" ? chosen?.name ?? "" : "", effort: nextEffort }, CODEX_PICKER_WAIT_MS, (failed) => {
        stopDriving.current = null;
        setDriving(false);
        if (!failed) return;
        setUnsent(failed === CODEX_NOT_SENT ? unsentWhy : failed);
        if (shouldShow) setChosenEngine((prior) => setting === "model" ? { ...prior, model: undefined } : { ...prior, effort: undefined });
      });
    }
    requestAnimationFrame(() => field.current?.focus());
  };
  // What this box saves as a board action types nothing into the terminal, so
  // a harness picker or a busy program does not stop it: those guard direct
  // typing, which is the path they were written for.
  const savesThroughBoard = queues || isHeld || isUnread || !!live?.isReconnecting;
  // While the harness is in a turn, or a screen of its own is open that
  // is no question to answer, such as its words for a usage limit, Modern
  // Chat's box types nothing into its terminal: the board holds the message
  // as his queued bubble at the bottom of the conversation, where he can
  // edit, send now or cancel it, and hands each over alone, in order, once
  // the harness's input is empty and takes it (the Overlord, 2026-10-27:
  // "queued should exist at the bottom of the conversation until sent").
  // Typed into such a screen it stayed on the prompt line unsent, and the
  // next one joined it there, so his messages reached the harness run
  // together as one (2026-10-28: "it's sending and appending messages
  // together that were sent previously").
  // It holds on what the harness's own screen says, in a turn or showing a
  // screen of its own, not on the task being under way, which a goblin's is
  // while its harness waits at an empty prompt. A prompt line that holds words
  // never sent, typed in the terminal itself or put back when the harness was
  // stopped, holds it too, since typed now it would join them. A box with no
  // conversation, Classic's or the deck's, holds at such a screen or line too,
  // through the board's queue it lists, and in a turn types into the
  // harness's own queue.
  const holdsForTurn = !isAsking && (isScreenOpen || !!live?.screen?.hasDraft || hasConversation && !!live?.screen?.isWorking);
  // A send the board has shown as its own action is no longer drawn from here.
  const sending = usePendingSends();
  useEffect(() => {
    for (const send of sending) if (snapshot.actions.some((action) => action.id === send.id)) removePendingSend(send.id);
  }, [sending, snapshot.actions]);
  const send = async () => {
    const text = draft.trim();
    if (!text || isSending || adding) return;
    // A setting the box has a control for opens the board's own pane, or is
    // set at once with the value typed after it, never the terminal's menu;
    // while those controls cannot change the session it stays in the box and
    // is never queued. A Codex model typed by its id keeps the effort a pick
    // of it in the pane would, when the harness's catalog lists it.
    const asked = settingCommand(harness, text);
    if (asked && !canChangeEngine) { setUnsent("The model and effort controls need a connected, unheld session."); return; }
    if (asked) {
      setDraft("");
      if (!asked.value) { setPane(asked.setting); return; }
      const chosen = harness === "codex" && asked.setting === "model" ? await request("/api/engines").then(parseEngineCatalog).then(
        (catalog) => catalog.find((item) => item.id === harness)?.models.find((listed) => listed.id === asked.value),
        (error: unknown) => { reportToCfo("the models of " + name, message(error)); return undefined; }) : undefined;
      applyEngine(asked.setting, asked.value, false, chosen);
      return;
    }
    if (!savesThroughBoard && !sendID.current && !holdsForTurn) {
      // A question of the harness's own takes what he wrote as one paste, on
      // one line and with no Enter, so no key a list binds acts on it before
      // he chose: the list's Enter confirms. It answers that question even
      // while the session works, and is never held as a message.
      if (isAsking) {
        if (live?.paste(text.replace(/\s*\n\s*/g, " "))) { setDraft(""); setUnsent(""); } else setUnsent(unsentWhy);
        return;
      }
      if (live?.send(text)) { setDraft(""); setUnsent(""); } else setUnsent(unsentWhy);
      return;
    }
    sendID.current ||= crypto.randomUUID();
    const id = sendID.current;
    // The conversation shows it at once as his queued bubble, and the box
    // empties, before the board has answered; a refusal takes the bubble
    // back and returns his words to the box as he wrote them.
    const written = draft;
    if (hasConversation) {
      addPendingSend({ id, task: task?.id || "", text });
      setDraft("");
    }
    setSending(true);
    try {
      await post({ id, kind: "message", task_id: task?.id || "", generation: "", text });
      sendID.current = "";
      storeDraft(draftKey, "");
      setDraft("");
      setUnsent("");
    } catch (error: unknown) {
      removePendingSend(id);
      setDraft((prior) => prior || written);
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
  // he stops typing, the last list for that same @ kept on screen meanwhile.
  const commandOptions = isListClosed ? [] : slashOptions(commands.data ?? [], draft);
  const mention = isListClosed || commandOptions.length ? null : mentionAt(draft, caret);
  const mentionStart = mention?.start ?? null;
  const mentionQuery = mention?.query ?? null;
  if (found.start !== mentionStart) setFound({ start: mentionStart, query: "", files: [] });
  const filesAddress = "/api/files?" + sessionQuery.toString();
  useEffect(() => {
    if (mentionStart === null || mentionQuery === null) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      request(filesAddress + "&q=" + encodeURIComponent(mentionQuery), controller.signal).then(parseFiles)
        .then((files) => setFound({ start: mentionStart, query: mentionQuery, files }), (error: unknown) => {
          if (controller.signal.aborted) return;
          setFound({ start: mentionStart, query: mentionQuery, files: [] });
          reportToCfo("the files of " + name, message(error));
        });
    }, MENTION_WAIT_MS);
    return () => { clearTimeout(timer); controller.abort(); };
  }, [mentionStart, mentionQuery, filesAddress, name]);
  const mentionFiles = mention ? mentionedFiles(found.files, found.query, mention.query) : [];
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
  // Up in an empty box, or on the first line of a message it recalled,
  // brings back what he sent before, newest first, as the terminal does, and
  // Down walks forward again to the empty box; any edit ends the recall.
  const recall = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    const box = event.currentTarget;
    const shown = recalled !== null && draft === history[recalled] ? recalled : null;
    if (event.key === "ArrowUp" && (!draft || shown !== null && !box.value.slice(0, box.selectionStart).includes("\n")) && history.length) {
      const at = Math.max(0, (shown ?? history.length) - 1);
      write(() => history[at]);
      setRecalled(at);
      setListClosed(true);
      return true;
    }
    if (event.key === "ArrowDown" && shown !== null && !box.value.slice(box.selectionEnd).includes("\n")) {
      const at = shown + 1;
      write(() => at < history.length ? history[at] : "");
      setRecalled(at < history.length ? at : null);
      setListClosed(true);
      return true;
    }
    return false;
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
    // Escape with the box's own pane, speech history or style menu open
    // closes it (the box's handler below), and reaches neither the harness
    // nor its screen.
    if (event.key === "Escape" && (pane || isRecentOpen || isStyleOpen)) return;
    // An open screen of the harness's own takes the terminal's keys from an
    // empty box, so it is answered here as in the terminal.
    const keysForPanel = panel && !isHeld && !draft ? panelKey(event, panel) : null;
    if (keysForPanel !== null) {
      event.preventDefault();
      if (!live?.control(keysForPanel)) setUnsent(unsentWhy);
      return;
    }
    if (!event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey && recall(event)) { event.preventDefault(); return; }
    if (event.key === "Escape" && canStop && !event.shiftKey) { event.preventDefault(); live?.escape(); return; }
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
  const note = terminalFeedback || (adding ? "Adding " + (adding > 1 ? adding + " files" : "the file") + "…" : live?.feedback || dictation.note || queueNote);
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
  // The queued messages sit at the very bottom of the conversation, below the
  // room of rows not drawn yet, so showing them is going to its end at once:
  // a smooth scroll toward a place estimated from rows not yet measured would
  // stop short as they are drawn.
  const revealQueued = () => {
    const queued = document.querySelector<HTMLElement>("[data-queued]");
    const conversation = queued?.closest<HTMLElement>(".conversation");
    if (conversation) conversation.scrollTop = conversation.scrollHeight;
    queued?.scrollIntoView({ block: "nearest" });
  };
  const canSend = !isSending && !adding && !!draft.trim();
  // While the harness works and the box is empty, the send button is Stop,
  // which presses Escape in its terminal as the terminal's own Esc does.
  const canStop = isWorking && !draft.trim() && !isSending && !!live && !isHeld;
  return <section className="composer" aria-label={"Messages to " + name}>
    {panel && !isHeld && !isTerminalShown && !isDriving && <HarnessPanel panel={panel} name={name} onShowTerminal={onShowTerminal}
      onKeys={(keys) => { if (!live?.control(keys)) setUnsent(unsentWhy); field.current?.focus(); }} />}
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
    <div ref={composerBox} className="composer-box" onKeyDown={(event) => {
      if (event.key !== "Escape") return;
      if (pane) { event.stopPropagation(); setPane(null); field.current?.focus(); }
      else if (isRecentOpen) { event.stopPropagation(); closeRecent(); }
      else if (isStyleOpen) { event.stopPropagation(); setStyleOpen(false); }
    }}>
      {listOptions.length > 0 && <PopupList label={commandOptions.length ? "Commands" : "Files"} options={listOptions} pick={picked} onChoose={choose} />}
      {isRecentOpen && <RecentDictations where="the message" anchor={composerBox} model={dictation.model} look="pane" onPaste={(text) => { insert(text); setRecentOpen(false); }}
        onClose={closeRecent} onEngine={() => setEngine((count) => count + 1)} />}
      <div className="composer-input">
        <textarea ref={field} aria-label={"Message " + name} placeholder={"Message " + name} rows={1} value={draft} disabled={isSending}
          onChange={(event) => { const value = event.target.value; setCaret(event.target.selectionStart); write(() => value); }} onSelect={(event) => setCaret(event.currentTarget.selectionStart)} onKeyDown={keys} onPaste={paste} onKeyUp={(event) => { dictation.key(event.nativeEvent); }} />
        <input ref={files} className="composer-file-input" type="file" multiple hidden aria-hidden="true" tabIndex={-1} onChange={added} />
        <div className="composer-row">
          <div className="composer-row-start">
            <button type="button" className="composer-control" aria-label="Add files" data-tip="Add files" disabled={adding > 0} onClick={() => files.current?.click()}><Icon name="plus" /></button>
            <button ref={menu} type="button" className="composer-control" aria-haspopup="true" aria-expanded={isStyleOpen} aria-label={"Style: " + style} data-tip={isStyleOpen ? undefined : "Chat style and speech history"}
              onClick={() => setStyleOpen((prior) => !prior)}><Icon name={style === "classic" ? "terminal" : "comment"} /></button>
            {isStyleOpen && <div className="composer-style-popover">
              <div className="composer-style-choices" role="group" aria-label="Chat style">
                <button type="button" aria-pressed={style === "modern"} onClick={() => { chooseStyle("modern"); setStyleOpen(false); field.current?.focus(); }}><Icon name="comment" />Modern</button>
                <button type="button" aria-pressed={style === "classic"} onClick={() => { chooseStyle("classic"); setStyleOpen(false); field.current?.focus(); }}><Icon name="terminal" />Classic</button>
              </div>
              <button type="button" onClick={() => { setStyleOpen(false); setRecentOpen(true); menu.current?.focus(); }}><Icon name="clock" />Speech history</button>
              {onOpenTerminal && <button type="button" onClick={() => { setStyleOpen(false); onOpenTerminal(); }}><Icon name="external" />Open in Windows Terminal</button>}
            </div>}
            {hasConversation && queued.length > 0 && <button type="button" className="composer-control queued-control" aria-label={queued.length + (queued.length === 1 ? " queued message" : " queued messages")} data-tip="Show the queued messages in the conversation" onClick={revealQueued}><Icon name="clock" /><span>{queued.length}</span></button>}
          </div>
          <div className="composer-row-end">
            {mode && <button type="button" className="composer-control mode-chip" aria-label={"Mode: " + mode + ". Shift+Tab changes it"} data-tip={"Mode: " + mode + " · Shift+Tab changes it"} disabled={isHeld}
              onMouseDown={(event) => event.preventDefault()} onClick={() => { if (!live?.control(PANEL_KEYS.backTab)) setUnsent(unsentWhy); field.current?.focus(); }}><Icon name="shield" /><span className="mode-chip-name">{MODE_NAMES[mode] ?? mode}</span></button>}
            <button ref={modelControl} type="button" className="composer-control engine-control" aria-label="Change model" aria-haspopup="menu" aria-expanded={pane === "model"} disabled={!canChangeEngine}
              data-tip={pane === "model" ? undefined : (modelName || "Model") + (canChangeEngine ? " · Choose the session's model" : " · Model controls need a connected, unheld composer")}
              onClick={() => setPane((open) => open === "model" ? null : "model")}>{harness && <HarnessMark harness={harness} />}{modelName || "Model"}</button>
            <button ref={effortControl} type="button" className="composer-control engine-control" aria-label="Change effort" aria-haspopup="menu" aria-expanded={pane === "effort"} disabled={!canChangeEngine}
              data-tip={pane === "effort" ? undefined : canChangeEngine ? "Effort · Choose the session's reasoning effort" : "Effort controls need a connected, unheld composer"}
              onClick={() => setPane((open) => open === "effort" ? null : "effort")}>{shownEffort ? <span className={"effort-level " + effortTone(shownEffort)}>{shownEffort}</span> : <Icon name="sparkle" />}</button>
            {pane && <EnginePane setting={pane} harness={harness} model={shownModel} effort={shownEffort} control={pane === "model" ? modelControl : effortControl} onPick={(setting, value, chosen) => applyEngine(setting, value, true, chosen)} onClose={closePane} />}
            {live?.isDictating ? <button type="button" className="voice-bubble recording" aria-label="Stop terminal dictation" data-tip="Stop dictating" onClick={live.stopDictation}><Icon name="mic" /></button> : <DictationButton dictation={dictation} />}
            {canStop ? <button type="button" className="composer-send stop" aria-label="Stop" data-tip="Stop · Esc" onClick={() => live?.escape()}><Icon name="stop-circle" /></button>
              : <button type="button" className="composer-send" aria-label={isSending ? "Sending" : "Send"} data-tip={isSending ? "Sending" : "Send · Enter"} data-active={canSend || undefined} disabled={!canSend} onClick={() => void send()}>
                <Icon name={isSending ? "clock" : "send"} />
              </button>}
          </div>
        </div>
      </div>
      {unsent ? <Notice tone="warning" role="alert" className="composer-refusal">{unsent}</Notice> : <p className="composer-note" role="status">{note}</p>}
    </div>
  </section>;
}
