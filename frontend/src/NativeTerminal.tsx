import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { message, request } from "./api";
import { object, string, type Session, type Task } from "./types";
import { Icon } from "./Icon";
import { TerminalEmpty } from "./TerminalEmpty";
import { bracketedPaste, clickJumper, clickJumps, endStep, ESTIMATED_CELL, fittedFontSize, gridToAsk, PANEL_RESIZED, previewScale, HISTORY_LINES, historyText, JUMP_TO_BOTTOM, judgeLines, liveWheel, panelGrid, queueInput, queueScroll, scrollAction, scrolledUp, scrollHeldReason, scrollsItself, sizeStep, typingHeldReason, wheelLines, wheelScroll, wheelTurn, type PaneCommand, type SizeEvent } from "./terminalInput";
import { fontSizeFor, storedFontSize, storeFontSize } from "./terminalStream";
import { terminalDocument } from "./terminalDocument";
import { clipboardInput, terminalKey } from "./terminal-keys";
import { useDictation } from "./useDictation";
import { useVoice } from "./useVoice";
import { VoiceBubble } from "./VoiceBubble";

const FALLBACK_FONT = '"Cascadia Mono", Consolas, monospace';
// A panel that changes size asks for its new grid at once, and while it keeps
// changing, such as a window being dragged, at most this often, so the pane
// follows the panel without the program redrawing on every frame.
const RESIZE_EVERY_MS = 40;

// One view stream of the pane: sized when it has taken the pane's control and
// sized it to the panel, or else observing the pane at the pane's own size.
type Connection = { sized: boolean; abort: AbortController; lease: string; frameSeq: number; full: boolean };

// The goblin's live Herdr pane cast into the board. Once shown, an open view
// takes the pane's control and keeps the pane sized to the panel at the chosen
// text size (Ctrl+Plus and Ctrl+Minus) and live, whether or not the board's
// window has the focus; only closing the view hands the size back. Until then,
// or after another client takes the pane over, it shows the pane at the size a
// Herdr window gives it, fitted whole to the panel. Taking control sends the
// program nothing but its size. Herdr sends only the live screen, so the wheel
// and Shift+PageUp open the pane's history, read from Herdr into a terminal of
// its own over the screen; scrolling down at its bottom, or typing, returns to
// the live screen.
// An input the supervisor refuses, or whose outcome is unknown, ends the view;
// it is never resent, and reconnecting starts from a fresh full screen.
export function NativeTerminal({ task, node, instance, visible, shown, focus = 0 }: { task?: Task; node?: Session; instance: string; visible: boolean; shown: boolean; focus?: number }) {
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
  const voice = useVoice(instance, task?.id || "cfo", shown);
  const dictation = useDictation((text) => { pasteText.current?.(text); voice.remember(text); }, voice.defers);
  const dictate = dictation.key;
  const taskID = task?.id || "", generation = task?.generation || "", session = node?.id || "";
  const cfo = !task && !node;
  useEffect(() => {
    if (!host.current || !pastHost.current || !visible || unavailable) return;
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
    // selfScrolls is whether the pane scrolls itself, as Claude Code's
    // fullscreen interface does: it keeps no history to read, so the wheel
    // sends it Herdr's wheel scroll instead. selfScrollChecked is when its
    // history was last read to judge that, 0 until a new connection's first
    // look lands, and selfScrollRows the grid it was judged against; judging
    // is the lease and grid a look is on its way for, and unjudged the lines
    // of each turn that waited for a look at the screen's grid.
    let selfScrolls = false, selfScrollChecked = 0, selfScrollRows = 0, judging = "";
    let unjudged: number[] = [];
    // scrolled is how many notches the board has sent a pane that scrolls
    // itself up, so a click can jump it back to its bottom; taken is
    // the scroll of each turn that took the pane, sent once it is taken.
    let scrolled = 0;
    let taken: PaneCommand[] = [];
    // refused is why the view's last take of the pane was refused, such as a
    // review gate owning it; until the Overlord comes back to the board or
    // types, the wheel does not ask for the pane again.
    let refused = "";
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
    // The panel's divider is being dragged: the screen keeps its grid until
    // the drag ends, so the pane is resized once, not on every move.
    const resizing = () => !!element.closest(".workspace.resizing");
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
    // size, or a screen drawn larger than the panel, asks at once for the grid
    // that fits, then at most every RESIZE_EVERY_MS while it keeps changing,
    // and once more when it holds still. Only the newest size waits to be sent.
    let regrid: ReturnType<typeof setTimeout> | undefined, again = false, asked: { cols: number; rows: number } | null = null;
    const askGrid = () => {
      if (!active?.sized || !lease || abort.signal.aborted) return;
      const { width, height } = room();
      const size = gridToAsk(panelGrid(width, height, font, cell()), { cols: term.cols, rows: term.rows }, asked);
      if (!size) return;
      asked = size;
      for (let index = queue.length - 1; index >= 0; index--) if (queue[index].type === "terminal.resize") queue.splice(index, 1);
      queue.push({ type: "terminal.resize", cols: size.cols, rows: size.rows });
      void flush();
    };
    const resizeSized = () => {
      if (regrid !== undefined) { again = true; return; }
      askGrid();
      const next = () => {
        if (!again) { regrid = undefined; return; }
        again = false;
        askGrid();
        regrid = setTimeout(next, RESIZE_EVERY_MS);
      };
      regrid = setTimeout(next, RESIZE_EVERY_MS);
    };
    term.onRender(() => {
      settle();
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      if (active?.sized && screen && !resizing() && (screen.offsetWidth > room().width || screen.offsetHeight > room().height)) resizeSized();
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
        scrolled = scrolledUp(scrolled, command);
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
    // Reads the pane's history and shows it scrolled by lines from its
    // bottom, which is the live screen as it was when read; lines up are
    // negative, and lines down show nothing. A pane that scrolls itself is
    // sent the lines instead.
    const openHistory = async (lines: number) => {
      if (reading || showing || !lease || abort.signal.aborted) return;
      reading = true;
      try {
        const answer = object(await request("/api/terminal/history", abort.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ lease, lines: HISTORY_LINES }) }));
        if (abort.signal.aborted || !lease) return;
        selfScrolls = scrollsItself(string(answer.text), term.rows, string(answer.agent));
        selfScrollChecked = Date.now();
        selfScrollRows = term.rows;
        if (selfScrolls) {
          scrollPane(lines);
          return;
        }
        if (lines >= 0) return;
        past.options.fontSize = term.options.fontSize;
        past.resize(term.cols, term.rows);
        past.reset();
        await new Promise<void>((resolve) => past.write(historyText(string(answer.text)), resolve));
        past.scrollToBottom();
        past.scrollLines(lines);
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
    // Only a view that holds the pane can scroll it; one that does not takes
    // it first, since the wheel is the Overlord working in the board, and
    // scrolls it by that turn once it holds it.
    const scrollPane = (lines: number) => {
      const command = wheelScroll(lines);
      if (!command || !active) return;
      const turn = wheelTurn({ sized: active.sized, refused: !!refused });
      if (turn === "refused") { setError(scrollHeldReason(refused)); return; }
      if (turn === "take") { taken.push(command); step("typed"); return; }
      queueScroll(queue, command);
      void flush();
    };
    // Reads a few lines of the pane to judge whether it scrolls itself,
    // beside whatever the wheel is doing; the turns that waited for a look
    // at the screen's grid then scroll the pane or open its history. A look
    // at a newer lease or grid replaces one still on its way.
    const judge = async () => {
      const from = lease, rows = term.rows, look = from + " " + rows;
      if (!lease || judging === look || abort.signal.aborted) return;
      judging = look;
      try {
        const answer = object(await request("/api/terminal/history", abort.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ lease: from, lines: judgeLines(rows) }) }));
        if (abort.signal.aborted || lease !== from || judging !== look) return;
        selfScrolls = scrollsItself(string(answer.text), rows, string(answer.agent));
        selfScrollChecked = Date.now();
        selfScrollRows = rows;
      } catch (e: unknown) {
        if (abort.signal.aborted || lease !== from || judging !== look) return;
        if (unjudged.length) setError(message(e));
        unjudged = [];
        return;
      } finally {
        if (judging === look) judging = "";
      }
      const turns = unjudged;
      unjudged = [];
      const lines = turns.reduce((sum, turn) => sum + turn, 0);
      if (selfScrolls) for (const turn of turns) scrollPane(turn);
      else if (lines < 0) void openHistory(lines);
    };
    // A turn of the wheel over the live screen never waits for a look at the
    // pane, except before the first at the screen's grid.
    const liveTurn = (lines: number) => {
      const turn = liveWheel({ at: selfScrollChecked, selfScrolls, rows: selfScrollRows }, { now: Date.now(), rows: term.rows }, lines);
      if (turn.action === "scroll") scrollPane(lines);
      if (turn.action === "open") void openHistory(lines);
      if (turn.action === "wait") unjudged.push(lines);
      if (turn.look) void judge();
    };
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
      if (event === "focus" || event === "typed") {
        held = false;
        if (refused) { refused = ""; setError(""); }
      }
      const action = sizeStep(event, { sized: pending ? pending.sized : active.sized, shown: shownValue.current, held });
      if (action === "take") want(true);
    };
    const send = (text: string) => {
      if (!text || !lease || abort.signal.aborted) return;
      setError("");
      step("typed");
      queueInput(queue, text);
      void flush();
    };
    term.onData(send);
    // Typing in the history types into the pane and returns to its live screen.
    past.onData((text) => { closeHistory(); send(text); });
    const typePaste = (text: string) => {
      try { const paste = bracketedPaste(text); closeHistory(); send(paste); }
      catch (error) { setError(message(error)); }
    };
    pasteText.current = typePaste;
    const paste = (event: ClipboardEvent) => {
      event.preventDefault(); event.stopImmediatePropagation();
      const input = clipboardInput(event);
      if (input && "text" in input) typePaste(input.text);
      else if (input) { closeHistory(); send(input.key); }
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
    // pointer is released; a plain click on the live screen of a pane the
    // board scrolled up jumps it to its bottom.
    let pressed: { x: number; y: number; button: number } | null = null;
    const jumper = clickJumper(() => send(JUMP_TO_BOTTOM));
    const release = (event: PointerEvent) => {
      copy();
      const from = pressed;
      pressed = null;
      if (!from) return;
      const click = { button: from.button, moved: Math.hypot(event.clientX - from.x, event.clientY - from.y), selected: term.hasSelection() };
      jumper.released(() => !showing && !!active && clickJumps(scrolled, click, { sized: active.sized, refused: !!refused }));
    };
    const startCopy = (event: PointerEvent) => {
      jumper.cancel();
      pressed = event.currentTarget === element ? { x: event.clientX, y: event.clientY, button: event.button } : null;
      window.addEventListener("pointerup", release, { once: true });
    };
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
      const shortcut = terminalKey(event, term, copy);
      if (shortcut !== null) return shortcut;
      if (zoom(event)) return false;
      // Shift+PageUp opens the pane's history a screen up.
      if (event.shiftKey && !event.ctrlKey && !event.altKey && (event.key === "PageUp" || event.key === "PageDown")) {
        event.preventDefault();
        if (event.type === "keydown" && event.key === "PageUp") void openHistory(-Math.max(1, term.rows - 1));
        return false;
      }
      return !!lease;
    });
    past.attachCustomKeyEventHandler((event) => {
      event.stopPropagation();
      const dictated = dictate(event);
      if (dictated !== null) return dictated;
      const shortcut = terminalKey(event, past, () => copy(past));
      if (shortcut !== null) return shortcut;
      if (event.shiftKey && event.key === "Escape") {
        event.preventDefault();
        if (event.type === "keydown") focusPill();
        return false;
      }
      const page = event.shiftKey && !event.ctrlKey && !event.altKey && (event.key === "PageUp" || event.key === "PageDown");
      if (page || event.key === "Escape") {
        event.preventDefault();
        if (event.type !== "keydown") return false;
        if (event.key === "Escape" || event.key === "PageDown" && atBottom()) closeHistory();
        else if (page) past.scrollLines((event.key === "PageUp" ? -1 : 1) * Math.max(1, past.rows - 1));
        return false;
      }
      return true;
    });
    // The wheel counts whole lines of the screen as drawn: up from the live
    // screen opens the history, and down at the history's bottom returns.
    let wheelRest = 0;
    const wheelStep = (fromHistory: boolean, event: WheelEvent) => {
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      const row = screen && term.rows ? screen.offsetHeight / term.rows : 16;
      const { lines, rest } = wheelLines(wheelRest, event.deltaY, event.deltaMode, row, term.rows);
      wheelRest = rest;
      return { action: scrollAction(fromHistory, atBottom(), lines), lines };
    };
    const wheel = (fromHistory: boolean) => (event: WheelEvent) => {
      const { action, lines } = wheelStep(fromHistory, event);
      if (action === "history") return true;
      event.preventDefault();
      if (!fromHistory) liveTurn(lines);
      if (action === "close") closeHistory();
      return false;
    };
    term.attachCustomWheelEventHandler(wheel(false));
    past.attachCustomWheelEventHandler(wheel(true));
    // A screen fitted to the panel may fill only part of it, so the wheel
    // works over the whole panel: what lands beside the screen scrolls as if
    // it landed on it.
    const wheelBeside = (fromHistory: boolean) => (event: WheelEvent) => {
      const screen = fromHistory ? past : term;
      if (screen.element?.contains(event.target as Node)) return;
      const { action, lines } = wheelStep(fromHistory, event);
      event.preventDefault();
      if (!fromHistory) liveTurn(lines);
      if (action === "history") screen.scrollLines(lines);
      if (action === "close") closeHistory();
    };
    const liveBeside = wheelBeside(false), historyBeside = wheelBeside(true);
    element.addEventListener("wheel", liveBeside, { passive: false });
    pastElement.addEventListener("wheel", historyBeside, { passive: false });
    const refit = () => { if (active?.sized) resizeSized(); else fit(); };
    // While the panel's divider is dragged the screen keeps its grid and is
    // shown scaled into the panel, and it refits once when the drag ends.
    const preview = () => {
      const screen = element.querySelector<HTMLElement>(".xterm-screen");
      if (!term.element || !screen) return;
      term.element.style.transformOrigin = "0 0";
      term.element.style.transform = "scale(" + previewScale(room(), { width: screen.offsetWidth, height: screen.offsetHeight }) + ")";
    };
    // The drag's end has committed the panel's final width by the next frame.
    const dropped = () => requestAnimationFrame(() => {
      if (abort.signal.aborted) return;
      if (term.element) term.element.style.transform = "";
      refit();
    });
    const resize = new ResizeObserver(() => { if (resizing()) preview(); else refit(); });
    resize.observe(element);
    window.addEventListener(PANEL_RESIZED, dropped);
    const focused = () => step("focus");
    window.addEventListener("focus", focused);
    shownChanged.current = (isShown) => { if (isShown) step("shown"); };
    // A connection takes the screen once no input is on its way through the
    // one it replaces, so no key's outcome is lost in the switch. A sized view
    // that ends, however it ends, has the supervisor return the pane to the
    // size Herdr lays it out at. A sized view checks its grid once it lands,
    // since the panel or text size may have changed while it was on its way.
    const activate = async (connection: Connection) => {
      while (flushing) await new Promise<void>((done) => idle.push(done));
      if (connection.abort.signal.aborted || abort.signal.aborted) return false;
      const previous = active;
      active = connection;
      if (pending === connection) pending = null;
      lease = connection.lease;
      seq = 0;
      selfScrollChecked = 0;
      // A size or a scroll asked of the connection it replaces means nothing
      // to this one, but the turns that took the pane scroll it now.
      for (let index = queue.length - 1; index >= 0; index--) if (queue[index].type !== "terminal.input") queue.splice(index, 1);
      asked = null;
      previous?.abort.abort();
      closeHistory();
      checking = false;
      if (connection.sized) {
        term.options.fontSize = font;
        resizeSized();
      }
      for (const command of connection.sized ? taken : []) queueScroll(queue, command);
      taken = [];
      void flush();
      return true;
    };
    // A connection that ends on its own is handled as endStep decides. A
    // sized one that ends waits for the Overlord to come back to the board
    // before sizing the pane again. Showing the pane afresh ends the
    // connection on screen, and a switch already on its way takes the screen
    // instead of a fresh connection.
    const ended = (connection: Connection, reason: string) => {
      if (connection.abort.signal.aborted) return;
      if (pending === connection) pending = null;
      const action = endStep({ sized: connection.sized, onScreen: connection === active, resized: /pane was resized/i.test(reason) }, active ? active.sized : null);
      if (action === "stop") { stop(reason); return; }
      if (connection.sized) held = true;
      if (connection.sized && connection !== active) { refused = reason; taken = []; }
      if (action === "keep") return;
      if (active) {
        active.abort.abort();
        active = null;
        lease = "";
        term.options.disableStdin = true;
        if (flushing || queue.some((command) => command.type === "terminal.input")) { stop("The pane changed while input was being sent, so that input's outcome is unknown and nothing was resent. Reconnect for a fresh screen."); return; }
        queue.length = 0;
      }
      if (!pending) void connect(false);
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
            if (connection !== active && !(await activate(connection))) return;
            connection.frameSeq = frame.seq;
            if (frame.full === true) term.reset();
            const rowsChanged = term.rows !== frame.height;
            if (term.cols !== frame.width || rowsChanged) { term.resize(frame.width, frame.height); if (showing) past.resize(frame.width, frame.height); fit(); }
            const bytes = Uint8Array.from(atob(string(frame.bytes)), (character) => character.charCodeAt(0));
            await new Promise<void>((resolve) => term.write(bytes, resolve));
            // The wheel's judgment is read beside the first whole screen, before
            // any turn needs it, and again on each new grid, since a judgment
            // holds only for the rows it was read at.
            if (connection.full) {
              if (rowsChanged) void judge();
              continue;
            }
            connection.full = true;
            term.options.disableStdin = false;
            void judge();
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
    return () => { lease = ""; liveValue.current = false; abort.abort(); queue.length = 0; clearTimeout(copiedTimer); clearTimeout(regrid); jumper.cancel(); resize.disconnect(); window.removeEventListener(PANEL_RESIZED, dropped); window.removeEventListener("focus", focused); shownChanged.current = null; element.removeEventListener("paste", paste, true); pastElement.removeEventListener("paste", paste, true); element.removeEventListener("pointerdown", startCopy); pastElement.removeEventListener("pointerdown", startCopy); element.removeEventListener("wheel", liveBeside); pastElement.removeEventListener("wheel", historyBeside); window.removeEventListener("pointerup", release); term.dispose(); past.dispose(); terminal.current = null; history.current = null; pasteText.current = null; setInHistory(false); };
  }, [taskID, generation, session, instance, visible, attempt, unavailable, dictate]);
  if (unavailable) return <TerminalEmpty text={error} />;
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
    <VoiceBubble voice={voice} listening={dictation.listening} level={dictation.level} onPaste={(text) => { pasteText.current?.(text); terminal.current?.focus(); }} />
    {error ? <p className="terminal-error" role="alert">{error}</p> : dictation.note && <p className="terminal-error" role="status">{dictation.note}</p>}
  </section>;
}
