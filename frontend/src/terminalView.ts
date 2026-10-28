import { type IDisposable, Terminal } from "@xterm/xterm";
import { drawOnGpu } from "./terminal-gpu";
import { terminalDocument } from "./terminalDocument";
import { copyText } from "./clipboard.ts";
import { attachDictatedInput } from "./dictated-input";
import { clipboardInput, imageKey, readClipboard, terminalKey } from "./terminal-keys";
import { FrameWriter } from "./terminalFrames";
import { stripPasteEscapes, wheelLines } from "./terminalInput";
import { ackDue, DEFAULT_FONT_SIZE, type FitEvent, type FitState, followFontSize, fontSizeFor, inputMessages, nextFit, panelFit, parseHeld, parseHistory, parseScreen, parseSize, parseUnread, type ScreenState } from "./terminalStream";
import { PinnedLine } from "./pinned-line";
import { attachZoomGesture } from "./terminal-zoom";
import { attachTouchScroll, terminalText } from "./terminal-touch";

export const FALLBACK_FONT = '"Cascadia Mono", Consolas, monospace';
export const THEME = { background: "#071015", foreground: "#d8e9e2", cursor: "#6ee7b7", selectionBackground: "#286856" };
// The even space kept between the panel's edges and the terminal's grid.
export const PADDING = 10;
export const LINE_HEIGHT = 1.2;
// A resize while the panel is being dragged reaches the pseudo console once
// the size has held for this long, so the program redraws once, not per frame.
const RESIZE_SETTLE_MS = 120;
// A view whose repaint never arrives is shown after this long all the same.
const READY_FALLBACK_MS = 600;
// Keys that only change the next key send nothing of their own.
const MODIFIER_KEYS = new Set(["Shift", "Control", "Alt", "AltGraph", "Meta", "CapsLock", "NumLock", "ScrollLock", "Fn"]);
// The mouse modes in which xterm hands the wheel to the program.
const WHEEL_MOUSE_MODES = new Set(["vt200", "drag", "any"]);

export interface ViewEvents {
  // The screen is whole: the history replayed and the repaint after it drawn.
  ready: () => void;
  closed: (code: number, reason: string) => void;
  copied: () => void;
  font: (size: number) => void;
  // Dictation sees every key first: false keeps it from the terminal, true
  // lets it through, and null means it is not dictation's.
  dictate: (event: KeyboardEvent) => boolean | null;
  harness: () => string;
  // A picture pasted on another device, to put on this PC's clipboard before
  // the harness's image key is pressed.
  image: (picture: File) => void;
  // What a paste the browser could not start met, such as a clipboard read it
  // refused, to say beside the terminal.
  problem: (why: string) => void;
  // While he reads the history, where it ends, the top of the panel's last
  // row in px from the panel's top, which Jump to bottom sits above; null at
  // the live end.
  reading: (end: number | null) => void;
  // Key presses have sat unread in the terminal's input, as a busy program
  // leaves them, or its program has read them.
  unread: (isUnread: boolean) => void;
  // Why typing is held while the screen stays live, as while a gate owns the
  // task, or an empty reason once it is free. Typing meanwhile reaches no
  // one: the relay drops it.
  held: (reason: string) => void;
  // A key was typed while the view had no open connection, so it was not
  // sent: a key sent once the view is back could act on a screen he no
  // longer sees.
  unsent: () => void;
  // The relay read the harness's screen anew: whether it waits on an answer
  // at a screen of its own, and whether something of its own is open.
  screen: (state: ScreenState) => void;
  // Full-screen programs own their history; while input is held or closed,
  // the conversation record is the readable history instead.
  history?: () => void;
}

// TerminalView is one connection to a native terminal and the xterm that
// draws it. It is staged, laid out but invisible, while it replays the host's
// history and the repaint its first size asks for, so the panel never shows a
// blank or half-drawn screen. It draws on the GPU where it can (drawOnGpu) and
// writes a whole redraw at a time.
export class TerminalView {
  readonly element: HTMLDivElement;
  private readonly term: Terminal;
  private readonly socket: WebSocket;
  private readonly frames: FrameWriter;
  private readonly resize: ResizeObserver;
  private readonly rendered: IDisposable;
  private readonly encoder = new TextEncoder();
  private readonly events: ViewEvents;
  // history is how many output bytes replay the terminal's history, once the
  // relay has said; the output after them is live.
  private history = -1;
  private written = 0;
  private consumed = 0;
  private acknowledged = 0;
  // owner is whether the terminal has the size this view last claimed, and
  // isTaken whether it has taken that size since. A size it takes after that
  // is another window's, which this view draws until typed into.
  private owner = true;
  private isTaken = false;
  private claimed: { cols: number; rows: number } | null = null;
  // claimedAt is how much output was drawn when the terminal first took this
  // view's size; the repaint that size asked for follows it.
  private claimedAt = -1;
  private sized = false;
  private isReady = false;
  // Whether key presses sit unread in the terminal's input, as the relay
  // last said, which may be before this view is whole.
  private isUnread = false;
  // Why typing is held, as the relay last said, or empty while it is free.
  private heldReason = "";
  // What the relay last said of the harness's screen, or null while this
  // view has not been told.
  private screenState: ScreenState | null = null;
  private isShown = false;
  private fit: FitState = { isDrawn: true, isClaimPending: false };
  private disposed = false;
  private frame = 0;
  private settle: ReturnType<typeof setTimeout> | undefined;
  private fallback: ReturnType<typeof setTimeout> | undefined;
  private readonly pin: PinnedLine;
  private pinFrame = 0;
  private historyEnd: number | null = null;
  // Whether the program shows its cursor, which the pinned line draws as it.
  private isCursorShown = true;
  private readonly stopFollowing: () => void;
  private readonly stopZooming: () => void;
  private readonly stopSwiping: () => void;
  private readonly detachDictatedInput: () => void;

  constructor(container: HTMLElement, url: URL, fontSize: number, events: ViewEvents) {
    this.events = events;
    this.element = document.createElement("div");
    this.element.className = "terminal-view staged";
    container.append(this.element);
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || "";
    this.term = new Terminal({ documentOverride: terminalDocument(nonce), fontSize, fontFamily: FALLBACK_FONT, lineHeight: LINE_HEIGHT, scrollback: 5000, cursorBlink: false, theme: THEME, linkHandler: { activate: () => {} } });
    this.term.open(this.element);
    drawOnGpu(this.term);
    this.term.textarea?.setAttribute("aria-label", "Terminal input");
    this.term.parser.registerOscHandler(52, () => true);
    this.pin = new PinnedLine(this.element);
    for (const [final, isShown] of [["h", true], ["l", false]] as const) {
      this.term.parser.registerCsiHandler({ prefix: "?", final }, (params) => {
        if (params.includes(25)) this.isCursorShown = isShown;
        return false;
      });
    }
    this.term.onScroll(() => this.repin());
    this.term.onWriteParsed(() => this.repin());
    this.term.onResize(() => this.repin());
    this.frames = new FrameWriter((data, done) => this.term.write(data, done), undefined, () => this.readyIfDrawn());
    void document.fonts.load(fontSize + 'px "JetBrains Mono"').then(() => {
      if (!this.disposed && document.fonts.check(fontSize + 'px "JetBrains Mono"')) { this.term.options.fontFamily = '"JetBrains Mono", ' + FALLBACK_FONT; this.refit(); }
    }, () => {});
    this.socket = new WebSocket(url);
    this.socket.binaryType = "arraybuffer";
    this.socket.onopen = () => this.claim();
    this.socket.onmessage = (event: MessageEvent<ArrayBuffer | string>) => this.receive(event.data);
    this.socket.onclose = (event) => { if (!this.disposed) { this.term.options.disableStdin = true; this.events.closed(event.code, event.reason); } };
    this.term.onData((data) => {
      // The claim may wait for the grid to be drawn; the claim then made takes
      // the terminal back all the same.
      if (!this.owner) { this.isTaken = false; this.claim(); }
      for (const piece of inputMessages(this.encoder.encode(data))) this.send(piece);
    });
    this.term.onBinary((data) => this.send(Uint8Array.from(data, (character) => character.charCodeAt(0) & 255)));
    this.detachDictatedInput = attachDictatedInput(this.term, (keys) => { if (this.isSendable()) this.term.input(keys, true); });
    this.element.addEventListener("pointerdown", this.startCopy);
    this.element.addEventListener("paste", this.pasteClipboard, true);
    this.term.element?.addEventListener("wheel", this.wheelHistory, { capture: true, passive: false });
    this.term.attachCustomKeyEventHandler((event) => this.key(event));
    let wheelRest = 0;
    this.term.attachCustomWheelEventHandler((event) => {
      if (!this.heldReason && this.socket.readyState === WebSocket.OPEN) return true;
      const movement = wheelLines(wheelRest, event.deltaY, event.deltaMode, this.cell().height, this.term.rows);
      wheelRest = movement.rest;
      this.term.scrollLines(movement.lines);
      event.preventDefault();
      return false;
    });
    this.rendered = this.term.onRender(() => { if (this.step("draw")) this.refit(); this.repin(); });
    this.resize = new ResizeObserver(() => this.refit());
    this.resize.observe(this.element);
    // A size chosen in any terminal, by the keys, the right button and the
    // wheel, or the wheel with Ctrl and Shift, is this one's too.
    this.stopFollowing = followFontSize((size) => this.setFont(size));
    this.stopZooming = attachZoomGesture(this.element, window, () => this.term.options.fontSize ?? DEFAULT_FONT_SIZE, (size) => { this.setFont(size); this.events.font(size); });
    this.stopSwiping = attachTouchScroll(this.element, () => this.cell().height, (lines, at) => this.swipe(lines, at));
  }

  get ready(): boolean { return this.isReady; }

  get unread(): boolean { return this.isUnread; }

  get held(): string { return this.heldReason; }

  get screen(): ScreenState | null { return this.screenState; }

  // show draws this view in the panel or keeps it live out of sight; a view
  // coming into sight is fitted to the panel and drawn again whole.
  show(shown: boolean): void {
    this.isShown = shown;
    if (!shown) return;
    this.claim();
    this.term.refresh(0, this.term.rows - 1);
  }

  promote(): void {
    this.element.classList.remove("staged");
  }

  focus(): void {
    this.term.focus();
  }

  // paste types text into the terminal the way a paste does, so a program
  // that asked for bracketed paste receives it as one, and says whether it
  // was sent.
  paste(text: string): boolean {
    if (!this.isSendable()) return false;
    this.term.paste(stripPasteEscapes(text));
    return true;
  }

  // enter presses Enter in the terminal, as a dictation stopped to send does
  // after typing its words, and says whether it was sent.
  enter(): boolean {
    if (!this.isSendable()) return false;
    this.term.input("\r", true);
    return true;
  }

  submit(text: string): string | null {
    if (!this.isReady || this.heldReason || this.isUnread || this.screenState?.isOpen || this.screenState?.hasDraft || !this.isSendable()) return null
    const submission = crypto.randomUUID()
    this.send(JSON.stringify({ type: "submit", submission }))
    if (!this.paste(text) || !this.enter()) return null
    return submission
  }

  // escape presses Escape in the terminal, which interrupts a harness's turn.
  escape(): boolean {
    if (!this.isSendable()) return false;
    this.term.input("\x1b", true);
    return true;
  }

  // pressImageKey presses the key the harness attaches this PC's clipboard
  // image on, once a picture is there.
  pressImageKey(): void {
    if (this.isSendable()) this.term.input(imageKey(this.events.harness()), true);
  }

  // text is what the terminal shows and holds above, as plain text.
  text(): string {
    return terminalText(this.term);
  }

  control(keys: string): boolean {
    if (!this.isReady || this.heldReason || !this.isSendable()) return false;
    this.term.input(keys, true);
    return true;
  }

  readingPosition(): { line: number; text: string } | null {
    const buffer = this.term.buffer.active;
    return buffer.type === "normal" && buffer.viewportY < buffer.baseY
      ? { line: buffer.viewportY, text: buffer.getLine(buffer.viewportY)?.translateToString() ?? "" } : null;
  }

  restoreReading(position: { line: number; text: string } | null): void {
    const buffer = this.term.buffer.active;
    if (!position || buffer.type !== "normal") return;
    let line = Math.min(position.line, buffer.baseY);
    if (buffer.getLine(line)?.translateToString() !== position.text) {
      let distance = Infinity;
      for (let at = 0; at <= buffer.baseY; at++) {
        if (buffer.getLine(at)?.translateToString() === position.text && Math.abs(at - position.line) < distance) {
          line = at;
          distance = Math.abs(at - position.line);
        }
      }
    }
    this.term.scrollToLine(line);
    this.repin();
  }

  // jumpToBottom returns to the live end and hands the terminal the keyboard.
  // xterm scrolls from the position its viewport last drew, which it brings
  // up to a resize only on its next frame, so a jump in between lands short
  // and goes again once xterm has drawn.
  jumpToBottom(): void {
    this.term.scrollToBottom();
    this.term.focus();
    const buffer = this.term.buffer.active;
    if (buffer.viewportY < buffer.baseY) requestAnimationFrame(() => { if (!this.disposed) this.term.scrollToBottom(); });
  }

  setFont(size: number): void {
    if (this.term.options.fontSize === size) return;
    this.term.options.fontSize = size;
    this.refit();
  }

  dispose(): void {
    this.disposed = true;
    cancelAnimationFrame(this.frame);
    cancelAnimationFrame(this.pinFrame);
    this.stopFollowing();
    this.stopZooming();
    this.stopSwiping();
    this.detachDictatedInput();
    this.pin.dispose();
    clearTimeout(this.settle);
    clearTimeout(this.fallback);
    this.resize.disconnect();
    this.rendered.dispose();
    this.frames.dispose();
    this.element.removeEventListener("pointerdown", this.startCopy);
    this.element.removeEventListener("paste", this.pasteClipboard, true);
    this.term.element?.removeEventListener("wheel", this.wheelHistory, true);
    window.removeEventListener("pointerup", this.copy);
    this.socket.onclose = null;
    this.socket.close(1000);
    this.term.dispose();
    this.element.remove();
  }

  private send(data: string | Uint8Array): void {
    if (this.socket.readyState === WebSocket.OPEN) this.socket.send(data);
  }

  private receive(data: ArrayBuffer | string): void {
    if (typeof data === "string") {
      const history = parseHistory(data);
      if (history !== null) { this.history = history; return; }
      const unread = parseUnread(data);
      if (unread !== null) { this.isUnread = unread; this.events.unread(unread); return; }
      const held = parseHeld(data);
      if (held !== null) { this.heldReason = held; this.events.held(held); return; }
      const screen = parseScreen(data);
      if (screen !== null) { this.screenState = screen; this.events.screen(screen); return; }
      // The terminal took a size here in its output. xterm parses writes in
      // order and calls back after each, so the grid changes once the output
      // before the size is drawn, and before the output after it.
      const size = parseSize(data);
      if (size) this.term.write("", () => this.took(size.cols, size.rows));
      return;
    }
    const bytes = new Uint8Array(data);
    this.written += bytes.length;
    this.frames.push(bytes, () => {
      this.consumed += bytes.length;
      if (ackDue(this.consumed, this.acknowledged, this.written)) { this.acknowledged = this.consumed; this.send(JSON.stringify({ type: "ack", bytes: this.consumed })); }
      this.readyIfDrawn();
    });
  }

  // The screen is whole once live output after the terminal took the view's
  // size, the repaint, is drawn and no synchronized update holds it back.
  private readyIfDrawn(): void {
    if (this.claimedAt >= 0 && this.history >= 0 && this.consumed > Math.max(this.history, this.claimedAt) && !this.frames.updating) this.markReady();
  }

  // took draws at the size the terminal took. Another view's size is drawn
  // until this view is typed into.
  private took(cols: number, rows: number): void {
    if (this.disposed) return;
    this.resizeGrid(cols, rows);
    this.owner = this.claimed?.cols === cols && this.claimed.rows === rows;
    if (this.owner) this.isTaken = true;
    if (this.owner && this.claimedAt < 0 && this.history >= 0 && this.consumed >= this.history) this.claimedAt = this.consumed;
    this.readyIfDrawn();
  }

  // A view whose connection closed before it was shown is never shown, its
  // wait for a repaint included: it has no terminal to draw, and an empty one
  // with a cursor reads as a program at work where its closing says why (a
  // goblin whose host a restart ended, 2026-10-20).
  private markReady(): void {
    if (this.isReady || this.disposed) return;
    clearTimeout(this.fallback);
    this.isReady = true;
    requestAnimationFrame(() => { if (!this.disposed && this.socket.readyState === WebSocket.OPEN) this.events.ready(); });
  }

  private refit(): void {
    cancelAnimationFrame(this.frame);
    this.frame = requestAnimationFrame(() => this.claim());
  }

  // claim sizes the terminal to this panel, which makes this view its owner.
  // The grid changes only when the terminal takes the size, at that point in
  // its output, so output written for the old size is never drawn on the new
  // grid. The first size repaints the screen even when it matches; later ones
  // reach the pseudo console once the panel has settled. Once another window
  // has sized the terminal after it took this view's size, only typing here
  // takes it back: this view becoming whole or coming into sight, its font
  // loading and its panel resizing draw that window's size.
  private claim(): void {
    if (this.disposed || !this.isShown || this.isTaken && !this.owner || !this.step("claim")) return;
    const panel = this.element.getBoundingClientRect();
    const size = panelFit(panel.width, panel.height, this.cell(), PADDING);
    if (!size || !this.term.element) return;
    this.term.element.style.padding = `${size.top}px ${size.right}px ${size.bottom}px ${size.left}px`;
    if (this.socket.readyState === WebSocket.CLOSED) { this.resizeGrid(size.cols, size.rows); return; }
    if (this.socket.readyState !== WebSocket.OPEN) return;
    if (this.sized && this.owner && size.cols === this.claimed?.cols && size.rows === this.claimed.rows) return;
    this.owner = true;
    this.isTaken = false;
    const claimed = { cols: size.cols, rows: size.rows };
    this.claimed = claimed;
    clearTimeout(this.settle);
    const report = () => this.send(JSON.stringify({ type: "resize", ...claimed }));
    if (this.sized) { this.settle = setTimeout(report, RESIZE_SETTLE_MS); return; }
    this.sized = true;
    report();
    this.fallback = setTimeout(() => this.markReady(), READY_FALLBACK_MS);
  }

  // repin draws the pinned line and tells where the history ends once a
  // frame, after whatever moved the screen, the cursor or the scroll.
  private repin(): void {
    if (this.pinFrame) return;
    this.pinFrame = requestAnimationFrame(() => {
      this.pinFrame = 0;
      if (this.disposed) return;
      const end = this.pin.update(this.term, this.isCursorShown);
      if (end === this.historyEnd) return;
      this.historyEnd = end;
      this.events.reading(end);
    });
  }

  // step moves the fit on by one event and says whether to claim now.
  private step(event: FitEvent): boolean {
    const { isClaim, ...fit } = nextFit(this.fit, event);
    this.fit = fit;
    return isClaim;
  }

  // resizeGrid resizes xterm, whose screen is measured again once drawn.
  private resizeGrid(cols: number, rows: number): void {
    if (cols === this.term.cols && rows === this.term.rows) return;
    this.step("resize");
    this.term.resize(cols, rows);
  }

  // cell is one character cell as xterm draws it: its drawn screen is exactly
  // its columns and rows of cells.
  private cell(): { width: number; height: number } {
    const screen = this.term.element?.querySelector(".xterm-screen")?.getBoundingClientRect();
    return screen ? { width: screen.width / this.term.cols, height: screen.height / this.term.rows } : { width: 0, height: 0 };
  }

  // swipe scrolls by a finger's lines as the wheel does: a program that takes
  // the wheel, by the mouse or on the alternate screen, gets a turn of it a
  // line, and otherwise the terminal's own history scrolls.
  private swipe(lines: number, at: { clientX: number; clientY: number }): void {
    const screen = this.term.element?.querySelector(".xterm-screen");
    if (!screen) return;
    if (this.term.buffer.active.type !== "normal" && (this.heldReason || this.socket.readyState !== WebSocket.OPEN)) {
      if (lines < 0) this.events.history?.();
      return;
    }
    if (this.term.buffer.active.type === "normal" && (!WHEEL_MOUSE_MODES.has(this.term.modes.mouseTrackingMode) || this.heldReason || this.socket.readyState !== WebSocket.OPEN)) { this.term.scrollLines(lines); return; }
    for (let turn = 0; turn < Math.abs(lines); turn++) screen.dispatchEvent(new WheelEvent("wheel", { deltaY: Math.sign(lines), deltaMode: WheelEvent.DOM_DELTA_LINE, clientX: at.clientX, clientY: at.clientY, bubbles: true, cancelable: true }));
  }

  private key(event: KeyboardEvent): boolean {
    // Escape and every other key belong to the terminal, never the panel.
    event.stopPropagation();
    const dictated = this.events.dictate(event);
    if (dictated !== null) return dictated;
    const down = event.type === "keydown";
    if (event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey && (event.key === "PageUp" || event.key === "PageDown")) {
      event.preventDefault();
      if (down) {
        const direction = event.key === "PageUp" ? -1 : 1;
        if (this.term.buffer.active.type === "normal") this.term.scrollPages(direction);
        else {
          const bounds = this.element.getBoundingClientRect();
          this.swipe(direction * Math.max(1, this.term.rows - 1), { clientX: bounds.x + bounds.width / 2, clientY: bounds.y + bounds.height / 2 });
        }
      }
      return false;
    }
    if (event.shiftKey && event.key === "Escape") {
      event.preventDefault();
      if (down) this.element.closest(".context-pane")?.querySelector<HTMLButtonElement>(".panel-top button:not(:disabled)")?.focus();
      return false;
    }
    if (down && event.altKey && !event.ctrlKey && !event.metaKey && event.key.toLowerCase() === "v") {
      event.preventDefault();
      void this.pasteClipboardKey();
      return false;
    }
    const shortcut = terminalKey(event, this.term, this.copy);
    if (shortcut !== null) return shortcut;
    if (event.key === "Enter" && event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey && !event.isComposing) {
      // Claude reads LF as Ctrl+J. Codex's Windows reader needs the native
      // Ctrl+J key down/up records: a bare LF loses its key identity in ConPTY.
      const harness = this.events.harness();
      const newline = harness === "claude" ? "\n" : harness === "codex" ? "\x1b[74;36;10;1;8;1_\x1b[74;36;10;0;8;1_" : null;
      if (newline !== null) {
        event.preventDefault();
        if (down && this.isSendable()) this.term.input(newline, true);
        return false;
      }
    }
    const size = event.ctrlKey && !event.altKey && !event.metaKey ? fontSizeFor(event.key, this.term.options.fontSize ?? DEFAULT_FONT_SIZE) : null;
    if (size !== null) {
      event.preventDefault();
      if (down) { this.setFont(size); this.events.font(size); }
      return false;
    }
    if (down && !MODIFIER_KEYS.has(event.key)) this.isSendable();
    return true;
  }

  // isSendable says whether typed input can reach the terminal now, and tells
  // of a key or paste that cannot.
  private isSendable(): boolean {
    if (this.socket.readyState === WebSocket.OPEN) return true;
    this.events.unsent();
    return false;
  }

  private readonly copy = (): void => {
    if (!this.term.hasSelection()) return;
    copyText(this.term.getSelection()).then(() => this.events.copied(), () => {});
  };

  // pasteClipboardKey pastes what the clipboard holds for Alt+V, which the
  // browser starts no paste event for: its text as a paste, its picture on
  // this PC's clipboard for the harness's image key, and a refusal said.
  private async pasteClipboardKey(): Promise<void> {
    const input = await readClipboard();
    if ("text" in input) { if (!this.paste(input.text)) this.isSendable(); }
    else if ("image" in input) this.events.image(input.image);
    else this.events.problem(input.error);
  }

  private readonly pasteClipboard = (event: ClipboardEvent): void => {
    event.preventDefault();
    event.stopImmediatePropagation();
    const input = clipboardInput(event, this.events.harness());
    if (input && "text" in input) this.paste(input.text);
    else if (input && "image" in input) this.events.image(input.image);
    else if (input && this.isSendable()) this.term.input(input.key, true);
  };

  // wheelHistory opens the history on a wheel up over a full-screen program
  // that takes no typing, held or closed, as a swipe does. It listens on
  // xterm's element ahead of xterm: xterm's scrollbar takes a wheel first
  // whenever it has room to scroll, as for a moment after the terminal's box
  // changes size, and xterm then never asks its wheel handler. The zoom
  // gesture, on the view's element above it, takes a right-button wheel, and
  // a wheel with Ctrl and Shift held, before it.
  private readonly wheelHistory = (event: WheelEvent): void => {
    if (this.term.buffer.active.type === "normal" || !this.heldReason && this.socket.readyState === WebSocket.OPEN) return;
    if (event.deltaY < 0) this.events.history?.();
    event.preventDefault();
    event.stopPropagation();
  };

  // Releasing a drag selection copies it, wherever the pointer is released.
  private readonly startCopy = (): void => window.addEventListener("pointerup", this.copy, { once: true });
}
