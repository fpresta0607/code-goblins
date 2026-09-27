import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { message, request } from "./api";
import { object, string, type Session, type Task } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { Icon } from "./Icon";
import { bracketedPaste, ESTIMATED_CELL, fittedFontSize, HISTORY_LINES, historyText, inputBytes, maxInputBytes, queueInput, scrollAction, typingHeldReason, wheelLines, type PaneCommand } from "./terminalInput";
import { terminalDocument } from "./terminalDocument";
import { useDictation } from "./useDictation";

const FALLBACK_FONT = '"Cascadia Mono", Consolas, monospace';

// The goblin's live Herdr pane cast into the board: a view stream whose frames
// arrive at the pane's own size, fitted whole to the panel, and whose lease
// takes typing straight away. Nothing here resizes the real pane. Herdr sends
// only the live screen, so the wheel and Shift+PageUp open the pane's history,
// read from Herdr into a terminal of its own over the screen; scrolling down
// at its bottom, or typing, returns to the live screen.
// An input the supervisor refuses, or whose outcome is unknown, ends the view;
// it is never resent, and reconnecting starts from a fresh full screen.
export function NativeTerminal({ task, node, instance, visible, shown, focus = 0, onOwner }: { task?: Task; node?: Session; instance: string; visible: boolean; shown: boolean; focus?: number; onOwner?: () => void }) {
  const host = useRef<HTMLDivElement>(null);
  const pastHost = useRef<HTMLDivElement>(null);
  const history = useRef<Terminal | null>(null);
  const terminal = useRef<Terminal | null>(null);
  const shownValue = useRef(shown);
  useEffect(() => { shownValue.current = shown; }, [shown]);
  // A switch to this terminal hands it the keyboard, at once or on its first
  // frame, giving it to the history while that is shown.
  const wantFocus = useRef(false);
  const liveValue = useRef(false);
  const inHistoryValue = useRef(false);
  useEffect(() => {
    if (!focus) return;
    const target = inHistoryValue.current ? history.current : terminal.current;
    if (target && liveValue.current && shownValue.current) target.focus(); else wantFocus.current = true;
  }, [focus]);
  const [live, setLive] = useState(false);
  const [status, setStatus] = useState("Connecting");
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const [inHistory, setInHistory] = useState(false);
  // The history takes the keyboard from the live screen once it is in sight,
  // since a terminal out of sight cannot be focused; a wheel over the screen
  // while typing elsewhere leaves the keyboard where it is.
  useEffect(() => {
    inHistoryValue.current = inHistory;
    if (inHistory && shownValue.current && document.activeElement === terminal.current?.textarea) history.current?.focus();
  }, [inHistory]);
  const [unavailable, setUnavailable] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const pasteText = useRef<((text: string) => void) | null>(null);
  const dictation = useDictation((text) => pasteText.current?.(text));
  const dictate = dictation.key;
  const taskID = task?.id || "", generation = task?.generation || "", session = node?.id || "";
  const cfo = !task && !node;
  const shared = !!node && !ownsTaskSession(node, task);
  const queued = !!task && !task.generation;
  const missing = !cfo && (shared || queued) || unavailable;
  useEffect(() => {
    if (!host.current || !pastHost.current || !visible || missing) return;
    const element = host.current, pastElement = pastHost.current;
    const abort = new AbortController();
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || "";
    const look = { documentOverride: terminalDocument(nonce), fontSize: 15, fontFamily: FALLBACK_FONT, cursorBlink: false, theme: { background: "#071015", foreground: "#d8e9e2", cursor: "#6ee7b7", selectionBackground: "#286856" }, linkHandler: { activate: () => {} } };
    const term = new Terminal({ ...look, scrollback: 0, disableStdin: true });
    term.open(element);
    terminal.current = term;
    // The pane's history, in a terminal of its own over the live screen, drawn
    // at the live screen's size and font.
    const past = new Terminal({ ...look, scrollback: HISTORY_LINES, disableStdin: false, cursorInactiveStyle: "none" });
    past.open(pastElement);
    history.current = past;
    past.textarea?.setAttribute("aria-label", "Terminal history");
    past.parser.registerOscHandler(52, () => true);
    let reading = false, showing = false;
    // The bundled face measures differently from the fallback, so switch once
    // it has loaded; the changed option makes xterm measure its cells again.
    void document.fonts.load('15px "JetBrains Mono"').then(() => {
      if (abort.signal.aborted || !document.fonts.check('15px "JetBrains Mono"')) return;
      term.options.fontFamily = past.options.fontFamily = '"JetBrains Mono", ' + FALLBACK_FONT;
      requestAnimationFrame(() => fit());
    }, () => {});
    term.textarea?.setAttribute("aria-label", "Terminal input");
    term.parser.registerOscHandler(52, () => true);
    let lease = "", seq = 0, frameSeq = 0, full = false, flushing = false;
    const queue: PaneCommand[] = [];
    let copiedTimer: ReturnType<typeof setTimeout> | undefined;
    // The font is fitted from the cell measured on the screen xterm drew, then
    // checked on the screen it draws next: rows round to whole pixels, which
    // can add a row's height across a tall pane, so a screen that overflows
    // the panel steps down until it fits whole. Checking only ever shrinks.
    let checking = false;
    const settle = () => {
      if (!checking) return;
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      const current = term.options.fontSize || 15;
      if (!screen || screen.offsetWidth <= element.clientWidth && screen.offsetHeight <= element.clientHeight || current <= 1) { checking = false; return; }
      term.options.fontSize = current - 0.5;
    };
    term.onRender(() => {
      settle();
      if (past.options.fontSize !== term.options.fontSize) past.options.fontSize = term.options.fontSize;
    });
    const fit = () => {
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      const current = term.options.fontSize || 15;
      const cell = screen && screen.offsetWidth > 0 ? { width: screen.offsetWidth / term.cols / current, height: screen.offsetHeight / term.rows / current } : ESTIMATED_CELL;
      const size = fittedFontSize(element.clientWidth, element.clientHeight, term.cols, term.rows, cell);
      if (size === null) return;
      checking = true;
      if (size === current) settle(); else term.options.fontSize = size;
    };
    const stop = (reason: string) => {
      lease = "";
      term.options.disableStdin = true;
      abort.abort();
      liveValue.current = false;
      setLive(false);
      setStatus("Disconnected");
      setError(typingHeldReason(reason));
    };
    // Typing goes one input at a time in order, each with the lease's next
    // number.
    const flush = async () => {
      if (flushing) return;
      flushing = true;
      while (queue.length && lease && !abort.signal.aborted) {
        const command = queue.shift()!;
        try {
          await request("/api/terminal/input", abort.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ lease, seq: ++seq, command }) });
        } catch (e: unknown) {
          if (!abort.signal.aborted) stop(message(e));
          break;
        }
      }
      flushing = false;
    };
    const closeHistory = () => {
      if (!showing) return;
      showing = false;
      setInHistory(false);
      past.clearSelection();
      if (document.activeElement === past.textarea) term.focus();
    };
    // Reads the pane's history and shows it scrolled up by lines from its
    // bottom, which is the live screen as it was when read.
    const openHistory = async (lines: number) => {
      if (reading || showing || !lease || abort.signal.aborted) return;
      reading = true;
      try {
        const answer = object(await request("/api/terminal/history", abort.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ lease, lines: HISTORY_LINES }) }));
        if (abort.signal.aborted || !lease) return;
        past.options.fontSize = term.options.fontSize;
        past.resize(term.cols, term.rows);
        past.reset();
        await new Promise<void>((resolve) => past.write(historyText(string(answer.text)), resolve));
        past.scrollToBottom();
        past.scrollLines(-lines);
        showing = true;
        setInHistory(true);
        setError("");
      } catch (e: unknown) {
        if (!abort.signal.aborted) setError(message(e));
      } finally {
        reading = false;
      }
    };
    const atBottom = () => past.buffer.active.viewportY >= past.buffer.active.baseY;
    const send = (text: string) => {
      if (!text || !lease || abort.signal.aborted) return;
      if (inputBytes(text) > maxInputBytes) { setError("Input exceeds 64 KiB. Use a smaller selection; nothing was sent."); return; }
      setError("");
      queueInput(queue, text);
      void flush();
    };
    term.onData(send);
    // Typing in the history types into the pane and returns to its live screen.
    past.onData((text) => { closeHistory(); send(text); });
    const typePaste = (text: string) => { closeHistory(); try { send(bracketedPaste(text)); } catch (e: unknown) { setError(message(e)); } };
    pasteText.current = typePaste;
    const paste = (event: ClipboardEvent) => {
      event.preventDefault(); event.stopImmediatePropagation();
      const text = event.clipboardData?.getData("text/plain");
      if (text) typePaste(text);
    };
    element.addEventListener("paste", paste, true);
    pastElement.addEventListener("paste", paste, true);
    const copy = (from: Terminal = showing ? past : term) => {
      if (!from.hasSelection()) return;
      navigator.clipboard.writeText(from.getSelection()).then(() => {
        setCopied(true);
        clearTimeout(copiedTimer);
        copiedTimer = setTimeout(() => setCopied(false), 1400);
      }, () => setError("Clipboard access was refused. Use the browser's copy command on selected text."));
    };
    // Releasing a drag selection copies it, the way Herdr does, wherever the
    // pointer is released.
    const release = () => copy();
    const startCopy = () => window.addEventListener("pointerup", release, { once: true });
    element.addEventListener("pointerdown", startCopy);
    pastElement.addEventListener("pointerdown", startCopy);
    const focusPill = () => element.closest(".context-pane")?.querySelector<HTMLButtonElement>(".panel-pill button[aria-pressed='true']")?.focus();
    term.attachCustomKeyEventHandler((event) => {
      // Escape belongs to the pane, never the surrounding panel.
      event.stopPropagation();
      const dictated = dictate(event);
      if (dictated !== null) return dictated;
      if (event.shiftKey && event.key === "Escape") {
        event.preventDefault();
        if (event.type === "keydown") focusPill();
        return false;
      }
      if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "c") {
        event.preventDefault();
        if (event.type === "keydown") copy();
        return false;
      }
      // Shift+PageUp opens the pane's history a screen up.
      if (event.shiftKey && !event.ctrlKey && !event.altKey && (event.key === "PageUp" || event.key === "PageDown")) {
        event.preventDefault();
        if (event.type === "keydown" && event.key === "PageUp") void openHistory(Math.max(1, term.rows - 1));
        return false;
      }
      return !!lease;
    });
    past.attachCustomKeyEventHandler((event) => {
      event.stopPropagation();
      const dictated = dictate(event);
      if (dictated !== null) return dictated;
      if (event.shiftKey && event.key === "Escape") {
        event.preventDefault();
        if (event.type === "keydown") focusPill();
        return false;
      }
      const page = event.shiftKey && !event.ctrlKey && !event.altKey && (event.key === "PageUp" || event.key === "PageDown");
      if (page || event.key === "Escape" || event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "c") {
        event.preventDefault();
        if (event.type !== "keydown") return false;
        if (event.key === "Escape" || event.key === "PageDown" && atBottom()) closeHistory();
        else if (page) past.scrollLines((event.key === "PageUp" ? -1 : 1) * Math.max(1, past.rows - 1));
        else copy(past);
        return false;
      }
      return true;
    });
    // The wheel counts whole lines of the screen as drawn: up from the live
    // screen opens the history, and down at the history's bottom returns.
    let wheelRest = 0;
    const wheel = (fromHistory: boolean) => (event: WheelEvent) => {
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      const row = screen && term.rows ? screen.offsetHeight / term.rows : 16;
      const { lines, rest } = wheelLines(wheelRest, event.deltaY, event.deltaMode, row, term.rows);
      wheelRest = rest;
      const action = scrollAction(fromHistory, atBottom(), lines);
      if (action === "history") return true;
      event.preventDefault();
      if (action === "open") void openHistory(Math.abs(lines));
      if (action === "close") closeHistory();
      return false;
    };
    term.attachCustomWheelEventHandler(wheel(false));
    past.attachCustomWheelEventHandler(wheel(true));
    const resize = new ResizeObserver(() => fit());
    resize.observe(element);
    const read = async () => {
      liveValue.current = false; setLive(false); setError(""); setStatus("Connecting");
      try {
        const response = await fetch("/api/terminal/stream", { signal: abort.signal, method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ task: taskID, session, generation }) });
        if (!response.ok) {
          const failure = object(await response.json());
          if (failure.code === "terminal_unavailable") setUnavailable(true);
          if (failure.code === "registration_stale") { stop(string(failure.error)); setStatus("CFO registration stale"); return; }
          throw new Error(string(failure.error));
        }
        if (!response.body) throw new Error("The pane's stream is unavailable.");
        const reader = response.body.getReader(), decoder = new TextDecoder();
        let buffer = "";
        while (!abort.signal.aborted) {
          const next = await reader.read();
          if (next.done) throw new Error("The pane's view ended. Reconnect for a fresh screen.");
          buffer += decoder.decode(next.value, { stream: true });
          if (buffer.length > 4 * 1024 * 1024) throw new Error("A pane frame exceeded its limit.");
          let end: number;
          while ((end = buffer.indexOf("\n")) >= 0) {
            const frame = object(JSON.parse(buffer.slice(0, end))); buffer = buffer.slice(end + 1);
            if (frame.type === "terminal.closed") {
              // Herdr laid the pane out at a new size: open it again, whole, at that size.
              if (/pane was resized/i.test(string(frame.reason))) {
                lease = "";
                term.options.disableStdin = true;
                if (flushing || queue.length) { stop("The pane was resized while input was being sent, so that input's outcome is unknown and nothing was resent. Reconnect for a fresh screen."); return; }
                setAttempt((prior) => prior + 1);
                return;
              }
              throw new Error(string(frame.reason));
            }
            if (frame.type === "terminal.ready") { lease = string(frame.lease); continue; }
            if (frame.type !== "terminal.frame") continue;
            if (!lease || frame.encoding !== "ansi" || typeof frame.seq !== "number" || (!full && frame.full !== true) || (full && frame.full !== true && frame.seq !== frameSeq + 1) || typeof frame.width !== "number" || typeof frame.height !== "number") throw new Error("The pane's screen fell out of step. Reconnect for a full screen.");
            frameSeq = frame.seq;
            if (frame.full === true) term.reset();
            if (term.cols !== frame.width || term.rows !== frame.height) { term.resize(frame.width, frame.height); if (showing) past.resize(frame.width, frame.height); fit(); }
            const bytes = Uint8Array.from(atob(string(frame.bytes)), (character) => character.charCodeAt(0));
            await new Promise<void>((resolve) => term.write(bytes, resolve));
            if (!full) {
              full = true;
              term.options.disableStdin = false;
              liveValue.current = true;
              setLive(true);
              setStatus("Live");
              if (shownValue.current && (wantFocus.current || element.closest(".context-pane")?.contains(document.activeElement))) (showing ? past : term).focus();
              wantFocus.current = false;
            }
          }
        }
      } catch (e: unknown) { if (!abort.signal.aborted) stop(message(e)); }
    };
    void read();
    return () => { lease = ""; liveValue.current = false; abort.abort(); queue.length = 0; clearTimeout(copiedTimer); resize.disconnect(); element.removeEventListener("paste", paste, true); pastElement.removeEventListener("paste", paste, true); element.removeEventListener("pointerdown", startCopy); pastElement.removeEventListener("pointerdown", startCopy); window.removeEventListener("pointerup", release); term.dispose(); past.dispose(); terminal.current = null; history.current = null; pasteText.current = null; setInHistory(false); };
  }, [taskID, generation, session, instance, visible, attempt, missing, dictate]);
  if (missing) return <div className="terminal-empty"><Icon name="terminal" /><p>{queued ? "This task has not started yet." : shared ? "This child has no separate terminal." : error}</p>{onOwner && shared && <button className="primary" onClick={onOwner}>Open owning task</button>}</div>;
  return <section className="native-terminal" aria-label={cfo ? "CFO terminal" : "Goblin terminal"}>
    <div className="terminal-surface" ref={host} />
    <div className={"terminal-surface terminal-history" + (inHistory && live ? "" : " away")} ref={pastHost} aria-hidden={!(inHistory && live)} />
    {!live && status === "Connecting" && <div className="terminal-cover" role="status"><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the terminal</p></div>}
    <div className="terminal-overlay">
      {/* A live pane shows nothing over the screen; only a change of state or
          a copy needs saying, and the cover says it is connecting. */}
      {(live || status !== "Connecting") && <span className={"terminal-state" + (live ? " live" : "") + (live && !copied && !inHistory ? " quiet" : "")} role="status">
        <span className="status-dot" />{copied ? "Copied" : live && inHistory ? "History: scroll down or press Esc for live" : status}
      </span>}
      {!live && status !== "Connecting" && <button className="icon-button raised" disabled={!visible} aria-label="Reconnect" data-tip="Reconnect" data-tip-align="end" onClick={() => setAttempt((prior) => prior + 1)}><Icon name="refresh" /></button>}
    </div>
    {dictation.listening && <span className="terminal-state live terminal-listening" role="status"><Icon name="mic" />Listening</span>}
    {error ? <p className="terminal-error" role="alert">{error}</p> : dictation.note && <p className="terminal-error" role="status">{dictation.note}</p>}
  </section>;
}
