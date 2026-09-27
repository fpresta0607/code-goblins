import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { message, request } from "./api";
import { object, string, type Session, type Task } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { Icon } from "./Icon";
import { bracketedPaste, ESTIMATED_CELL, fittedFontSize, HISTORY_LINES, historyText, inputBytes, maxInputBytes, panelGrid, queueInput, scrollAction, sizeStep, typingHeldReason, wheelLines, type PaneCommand, type SizeEvent } from "./terminalInput";
import { fontSizeFor, storedFontSize, storeFontSize } from "./terminalStream";
import { terminalDocument } from "./terminalDocument";
import { useDictation } from "./useDictation";

const FALLBACK_FONT = '"Cascadia Mono", Consolas, monospace';
// A panel being dragged asks for its new size once it has held this long, so
// the program redraws once, not per frame.
const RESIZE_SETTLE_MS = 120;

// One view stream of the pane: sized when it has taken the pane's control and
// sized it to the panel, or else observing the pane at the pane's own size.
type Connection = { sized: boolean; abort: AbortController; lease: string; frameSeq: number; full: boolean };

// The goblin's live Herdr pane cast into the board. While the board's window
// has the focus and shows the pane, the view takes the pane's control and
// sizes the pane to the panel at the chosen text size (Ctrl+Plus and
// Ctrl+Minus); otherwise it shows the pane at the size a Herdr window gives
// it, fitted whole to the panel. Taking control sends the program nothing but
// its size. Herdr sends only the live screen, so the wheel and Shift+PageUp
// open the pane's history, read from Herdr into a terminal of its own over the
// screen; scrolling down at its bottom, or typing, returns to the live screen.
// An input the supervisor refuses, or whose outcome is unknown, ends the view;
// it is never resent, and reconnecting starts from a fresh full screen.
export function NativeTerminal({ task, node, instance, visible, shown, focus = 0, onOwner }: { task?: Task; node?: Session; instance: string; visible: boolean; shown: boolean; focus?: number; onOwner?: () => void }) {
  const host = useRef<HTMLDivElement>(null);
  const pastHost = useRef<HTMLDivElement>(null);
  const history = useRef<Terminal | null>(null);
  const terminal = useRef<Terminal | null>(null);
  const shownValue = useRef(shown);
  const shownChanged = useRef<((shown: boolean) => void) | null>(null);
  useEffect(() => { shownValue.current = shown; shownChanged.current?.(shown); }, [shown]);
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
      requestAnimationFrame(() => { if (active?.sized) resizeSized(); else fit(); });
    }, () => {});
    term.textarea?.setAttribute("aria-label", "Terminal input");
    term.parser.registerOscHandler(52, () => true);
    let lease = "", seq = 0, flushing = false, identity = "";
    const queue: PaneCommand[] = [];
    const idle: (() => void)[] = [];
    let copiedTimer: ReturnType<typeof setTimeout> | undefined;
    // The screen shows the active connection's frames and its lease takes
    // typing. A switch to or from sizing the pane opens beside it and takes
    // over on its first whole screen, so the screen never blanks.
    let active: Connection | null = null, pending: Connection | null = null;
    // held is whether another client took the pane from this view, or a
    // sized view was refused; the view then shows the pane at its own size
    // until the Overlord comes back to the board or types in it.
    let held = false;
    let font = storedFontSize();
    // The font is fitted from the cell measured on the screen xterm drew, then
    // checked on the screen it draws next: rows round to whole pixels, which
    // can add a row's height across a tall pane, so a screen that overflows
    // the panel steps down until it fits whole. Checking only ever shrinks.
    let checking = false;
    // The room the screen has: the panel inside its even inset.
    const room = () => {
      const style = getComputedStyle(element);
      return { width: element.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight), height: element.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom) };
    };
    const settle = () => {
      if (!checking) return;
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      const current = term.options.fontSize || 15;
      const { width, height } = room();
      if (!screen || screen.offsetWidth <= width && screen.offsetHeight <= height || current <= 1) { checking = false; return; }
      term.options.fontSize = current - 0.5;
    };
    const cell = () => {
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      const current = term.options.fontSize || 15;
      return screen && screen.offsetWidth > 0 && term.cols > 0 && term.rows > 0 ? { width: screen.offsetWidth / term.cols / current, height: screen.offsetHeight / term.rows / current } : ESTIMATED_CELL;
    };
    const fit = () => {
      if (active?.sized) return;
      const current = term.options.fontSize || 15;
      const { width, height } = room();
      const size = fittedFontSize(width, height, term.cols, term.rows, cell());
      if (size === null) return;
      checking = true;
      if (size === current) settle(); else term.options.fontSize = size;
    };
    // A sized view keeps the pane filling the panel: a changed panel or text
    // size, or a screen drawn larger than the panel, asks for the grid that
    // fits once the panel has held still.
    let regrid: ReturnType<typeof setTimeout> | undefined;
    const resizeSized = () => {
      clearTimeout(regrid);
      regrid = setTimeout(() => {
        if (!active?.sized || !lease || abort.signal.aborted) return;
        const { width, height } = room();
        const size = panelGrid(width, height, font, cell());
        if (!size || size.cols === term.cols && size.rows === term.rows) return;
        queue.push({ type: "terminal.resize", cols: size.cols, rows: size.rows });
        void flush();
      }, RESIZE_SETTLE_MS);
    };
    term.onRender(() => {
      settle();
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      if (active?.sized && screen && (screen.offsetWidth > room().width || screen.offsetHeight > room().height)) resizeSized();
      if (past.options.fontSize !== term.options.fontSize) past.options.fontSize = term.options.fontSize;
    });
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
      for (const done of idle.splice(0)) done();
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
    // want switches the view to sizing the pane or to showing it at its own
    // size, unless it is already there or on its way.
    const want = (sized: boolean) => {
      if (pending) {
        if (pending.sized === sized) return;
        pending.abort.abort();
        pending = null;
      }
      if (active && active.sized !== sized) void connect(sized);
    };
    const step = (event: SizeEvent) => {
      if (!active || abort.signal.aborted) return;
      if (event === "focus" || event === "typed") held = false;
      const action = sizeStep(event, { sized: pending ? pending.sized : active.sized, focused: document.hasFocus(), shown: shownValue.current, held });
      if (action !== "stay") want(action === "take");
    };
    const send = (text: string) => {
      if (!text || !lease || abort.signal.aborted) return;
      if (inputBytes(text) > maxInputBytes) { setError("Input exceeds 64 KiB. Use a smaller selection; nothing was sent."); return; }
      setError("");
      step("typed");
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
    // Ctrl+Plus, Ctrl+Minus and Ctrl+0 choose the text size a sized pane is
    // drawn at, and choosing it takes the pane's size.
    const zoom = (event: KeyboardEvent) => {
      const size = event.ctrlKey && !event.altKey && !event.metaKey ? fontSizeFor(event.key, font) : null;
      if (size === null) return false;
      event.preventDefault();
      if (event.type !== "keydown") return true;
      font = size;
      storeFontSize(size);
      if (active?.sized) {
        term.options.fontSize = size;
        resizeSized();
      } else {
        step("typed");
      }
      return true;
    };
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
      if (zoom(event)) return false;
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
    const resize = new ResizeObserver(() => { if (active?.sized) resizeSized(); else fit(); });
    resize.observe(element);
    const focused = () => step("focus");
    const blurred = () => step("blur");
    window.addEventListener("focus", focused);
    window.addEventListener("blur", blurred);
    shownChanged.current = (isShown) => step(isShown ? "shown" : "hidden");
    // A connection takes the screen once no input is on its way through the
    // one it replaces, so no key's outcome is lost in the switch. A sized view
    // handing the pane back first returns it to the size Herdr lays it out at,
    // which the new view shows, as a Herdr window does when a controller
    // leaves; with no Herdr window open, nothing else would.
    const activate = async (connection: Connection, layout: { cols: number; rows: number }) => {
      while (flushing) await new Promise<void>((done) => idle.push(done));
      if (connection.abort.signal.aborted || abort.signal.aborted) return false;
      const previous = active;
      if (previous?.sized && !connection.sized && lease) {
        flushing = true;
        try {
          await request("/api/terminal/input", abort.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ lease, seq: ++seq, command: { type: "terminal.resize", cols: layout.cols, rows: layout.rows } }) });
        } catch {
          // The pane keeps the board's size until a Herdr window takes it; the
          // view it hands over to still shows the pane.
        }
        flushing = false;
        for (const done of idle.splice(0)) done();
        if (connection.abort.signal.aborted || abort.signal.aborted) return false;
      }
      active = connection;
      if (pending === connection) pending = null;
      lease = connection.lease;
      seq = 0;
      // A size asked of the connection it replaces means nothing to this one.
      for (let index = queue.length - 1; index >= 0; index--) if (queue[index].type === "terminal.resize") queue.splice(index, 1);
      previous?.abort.abort();
      closeHistory();
      checking = false;
      if (connection.sized) term.options.fontSize = font;
      void flush();
      return true;
    };
    // The connection on screen that ends hands the screen on: a sized view
    // falls back to the pane's own size, and waits for the Overlord to come
    // back to the board before sizing it again; a pane Herdr resized, as the
    // board's own sizing does, is shown again whole at its new size; any other
    // end stops the view with the reason. A switch already on its way takes
    // the screen instead of a fresh connection. A sized connection refused
    // before it took the screen leaves the screen as it is.
    const ended = (connection: Connection, reason: string) => {
      if (connection.abort.signal.aborted) return;
      if (connection === active) {
        if (!connection.sized && !/pane was resized/i.test(reason)) { stop(reason); return; }
        active = null;
        lease = "";
        term.options.disableStdin = true;
        if (flushing || queue.some((command) => command.type === "terminal.input")) { stop("The pane changed while input was being sent, so that input's outcome is unknown and nothing was resent. Reconnect for a fresh screen."); return; }
        queue.length = 0;
        if (connection.sized) held = true;
        if (!pending) void connect(false);
        return;
      }
      if (pending === connection) pending = null;
      if (!connection.sized) { if (!active) stop(reason); return; }
      held = true;
      if (!active) void connect(false);
    };
    const connect = async (sized: boolean) => {
      const connection: Connection = { sized, abort: new AbortController(), lease: "", frameSeq: 0, full: false };
      abort.signal.addEventListener("abort", () => connection.abort.abort(), { once: true });
      pending = connection;
      const replacing = !!active;
      if (!replacing) { liveValue.current = false; setLive(false); setError(""); setStatus("Connecting"); }
      const size = sized ? panelGrid(room().width, room().height, font, cell()) : null;
      if (sized && !size) { if (pending === connection) pending = null; return; }
      try {
        const response = await fetch("/api/terminal/stream", { signal: connection.abort.signal, method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ task: taskID, session, generation, ...(size ? { control: true, identity, cols: size.cols, rows: size.rows } : {}) }) });
        if (!response.ok) {
          const failure = object(await response.json());
          if (!replacing && failure.code === "terminal_unavailable") setUnavailable(true);
          if (!replacing && failure.code === "registration_stale") { stop(string(failure.error)); setStatus("CFO registration stale"); return; }
          throw new Error(string(failure.error));
        }
        if (!response.body) throw new Error("The pane's stream is unavailable.");
        const reader = response.body.getReader(), decoder = new TextDecoder();
        let buffer = "";
        while (!connection.abort.signal.aborted) {
          const next = await reader.read();
          if (next.done) throw new Error("The pane's view ended. Reconnect for a fresh screen.");
          buffer += decoder.decode(next.value, { stream: true });
          if (buffer.length > 4 * 1024 * 1024) throw new Error("A pane frame exceeded its limit.");
          let end: number;
          while ((end = buffer.indexOf("\n")) >= 0) {
            const frame = object(JSON.parse(buffer.slice(0, end))); buffer = buffer.slice(end + 1);
            if (frame.type === "terminal.closed") { ended(connection, string(frame.reason)); return; }
            if (frame.type === "terminal.ready") {
              connection.lease = string(frame.lease);
              if (!sized) identity = string(frame.identity);
              continue;
            }
            if (frame.type !== "terminal.frame") continue;
            if (!connection.lease || frame.encoding !== "ansi" || typeof frame.seq !== "number" || (!connection.full && frame.full !== true) || (connection.full && frame.full !== true && frame.seq !== connection.frameSeq + 1) || typeof frame.width !== "number" || typeof frame.height !== "number") throw new Error("The pane's screen fell out of step. Reconnect for a full screen.");
            if (connection !== active && !(await activate(connection, { cols: frame.width, rows: frame.height }))) return;
            connection.frameSeq = frame.seq;
            if (frame.full === true) term.reset();
            if (term.cols !== frame.width || term.rows !== frame.height) { term.resize(frame.width, frame.height); if (showing) past.resize(frame.width, frame.height); fit(); }
            const bytes = Uint8Array.from(atob(string(frame.bytes)), (character) => character.charCodeAt(0));
            await new Promise<void>((resolve) => term.write(bytes, resolve));
            if (connection.full) continue;
            connection.full = true;
            term.options.disableStdin = false;
            if (!liveValue.current) {
              liveValue.current = true;
              setLive(true);
              setStatus("Live");
              if (shownValue.current && (wantFocus.current || element.closest(".context-pane")?.contains(document.activeElement))) (showing ? past : term).focus();
              wantFocus.current = false;
            }
            if (sized) continue;
            fit();
            step("live");
          }
        }
      } catch (e: unknown) { if (!connection.abort.signal.aborted) ended(connection, message(e)); }
    };
    void connect(false);
    return () => { lease = ""; liveValue.current = false; abort.abort(); queue.length = 0; clearTimeout(copiedTimer); clearTimeout(regrid); resize.disconnect(); window.removeEventListener("focus", focused); window.removeEventListener("blur", blurred); shownChanged.current = null; element.removeEventListener("paste", paste, true); pastElement.removeEventListener("paste", paste, true); element.removeEventListener("pointerdown", startCopy); pastElement.removeEventListener("pointerdown", startCopy); window.removeEventListener("pointerup", release); term.dispose(); past.dispose(); terminal.current = null; history.current = null; pasteText.current = null; setInHistory(false); };
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
