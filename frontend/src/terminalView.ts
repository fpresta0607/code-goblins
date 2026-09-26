import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import { terminalDocument } from "./terminalDocument";
import { FrameWriter } from "./terminalFrames";
import { ackDue, DEFAULT_FONT_SIZE, fontSizeFor, inputMessages, parseHistory, parseSize, usableSize } from "./terminalStream";

const FALLBACK_FONT = '"Cascadia Mono", Consolas, monospace';
const THEME = { background: "#071015", foreground: "#d8e9e2", cursor: "#6ee7b7", selectionBackground: "#286856" };
// A resize while the panel is being dragged reaches the pseudo console once
// the size has held for this long, so the program redraws once, not per frame.
const RESIZE_SETTLE_MS = 120;
// A view whose repaint never arrives is shown after this long all the same.
const READY_FALLBACK_MS = 600;

export interface ViewEvents {
  // The screen is whole: the history replayed and the repaint after it drawn.
  ready: () => void;
  closed: (code: number, reason: string) => void;
  copied: () => void;
  font: (size: number) => void;
  // Dictation sees every key first: false keeps it from the terminal, true
  // lets it through, and null means it is not dictation's.
  dictate: (event: KeyboardEvent) => boolean | null;
}

// TerminalView is one connection to a native terminal and the xterm that
// draws it. It is staged, laid out but invisible, while it replays the host's
// history and the repaint its first size asks for, so the panel never shows a
// blank or half-drawn screen. It draws on the GPU where it can and writes a
// whole redraw at a time.
export class TerminalView {
  readonly element: HTMLDivElement;
  private readonly term: Terminal;
  private readonly fit = new FitAddon();
  private readonly socket: WebSocket;
  private readonly frames: FrameWriter;
  private readonly resize: ResizeObserver;
  private readonly encoder = new TextEncoder();
  private readonly events: ViewEvents;
  // history is how many output bytes replay the terminal's history, once the
  // relay has said; the output after them is live.
  private history = -1;
  private written = 0;
  private consumed = 0;
  private acknowledged = 0;
  private owner = true;
  private sized = false;
  private isReady = false;
  private isShown = false;
  private disposed = false;
  private frame = 0;
  private settle: ReturnType<typeof setTimeout> | undefined;
  private fallback: ReturnType<typeof setTimeout> | undefined;

  constructor(container: HTMLElement, url: URL, fontSize: number, events: ViewEvents) {
    this.events = events;
    this.element = document.createElement("div");
    this.element.className = "terminal-view staged";
    container.append(this.element);
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || "";
    this.term = new Terminal({ documentOverride: terminalDocument(nonce), fontSize, fontFamily: FALLBACK_FONT, lineHeight: 1.2, scrollback: 5000, cursorBlink: false, theme: THEME, linkHandler: { activate: () => {} } });
    this.term.loadAddon(this.fit);
    this.term.open(this.element);
    try {
      const gpu = new WebglAddon();
      gpu.onContextLoss(() => gpu.dispose());
      this.term.loadAddon(gpu);
    } catch {
      // No WebGL here: xterm keeps drawing with its DOM renderer.
    }
    this.term.textarea?.setAttribute("aria-label", "Terminal input");
    this.term.parser.registerOscHandler(52, () => true);
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
      if (!this.owner) this.claim();
      for (const piece of inputMessages(this.encoder.encode(data))) this.send(piece);
    });
    this.term.onBinary((data) => this.send(Uint8Array.from(data, (character) => character.charCodeAt(0) & 255)));
    this.element.addEventListener("pointerdown", this.startCopy);
    this.term.attachCustomKeyEventHandler((event) => this.key(event));
    this.resize = new ResizeObserver(() => this.refit());
    this.resize.observe(this.element);
  }

  get ready(): boolean { return this.isReady; }

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
  // that asked for bracketed paste receives it as one.
  paste(text: string): void {
    this.term.paste(text);
  }

  setFont(size: number): void {
    if (this.term.options.fontSize === size) return;
    this.term.options.fontSize = size;
    this.refit();
  }

  dispose(): void {
    this.disposed = true;
    cancelAnimationFrame(this.frame);
    clearTimeout(this.settle);
    clearTimeout(this.fallback);
    this.resize.disconnect();
    this.frames.dispose();
    this.element.removeEventListener("pointerdown", this.startCopy);
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
      // Another view sized the terminal: draw at its size until typed into.
      const size = parseSize(data);
      if (size && (size.cols !== this.term.cols || size.rows !== this.term.rows)) { this.owner = false; this.term.resize(size.cols, size.rows); }
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

  // The screen is whole once live output after the view's size, the repaint,
  // is drawn and no synchronized update holds it back.
  private readyIfDrawn(): void {
    if (this.sized && this.history >= 0 && this.consumed > this.history && !this.frames.updating) this.markReady();
  }

  private markReady(): void {
    if (this.isReady || this.disposed) return;
    clearTimeout(this.fallback);
    this.isReady = true;
    requestAnimationFrame(() => { if (!this.disposed) this.events.ready(); });
  }

  private refit(): void {
    cancelAnimationFrame(this.frame);
    this.frame = requestAnimationFrame(() => this.claim());
  }

  // claim sizes the terminal to this panel, which makes this view its owner.
  // The first size repaints the screen even when it matches; later ones reach
  // the pseudo console once the panel has settled.
  private claim(): void {
    if (this.disposed || !this.isShown) return;
    const size = this.fit.proposeDimensions();
    if (!size || !usableSize(size.cols, size.rows)) return;
    if (this.socket.readyState === WebSocket.CLOSED) { this.term.resize(size.cols, size.rows); return; }
    if (this.socket.readyState !== WebSocket.OPEN) return;
    if (this.sized && this.owner && size.cols === this.term.cols && size.rows === this.term.rows) return;
    this.owner = true;
    if (size.cols !== this.term.cols || size.rows !== this.term.rows) this.term.resize(size.cols, size.rows);
    clearTimeout(this.settle);
    const report = () => this.send(JSON.stringify({ type: "resize", cols: this.term.cols, rows: this.term.rows }));
    if (this.sized) { this.settle = setTimeout(report, RESIZE_SETTLE_MS); return; }
    this.sized = true;
    report();
    this.fallback = setTimeout(() => this.markReady(), READY_FALLBACK_MS);
  }

  private key(event: KeyboardEvent): boolean {
    // Escape and every other key belong to the terminal, never the panel.
    event.stopPropagation();
    const dictated = this.events.dictate(event);
    if (dictated !== null) return dictated;
    const down = event.type === "keydown";
    if (event.shiftKey && event.key === "Escape") {
      event.preventDefault();
      if (down) this.element.closest(".context-pane")?.querySelector<HTMLButtonElement>(".panel-pill button[aria-pressed='true']")?.focus();
      return false;
    }
    if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "c") {
      event.preventDefault();
      if (down) this.copy();
      return false;
    }
    const size = event.ctrlKey && !event.altKey && !event.metaKey ? fontSizeFor(event.key, this.term.options.fontSize ?? DEFAULT_FONT_SIZE) : null;
    if (size !== null) {
      event.preventDefault();
      if (down) { this.setFont(size); this.events.font(size); }
      return false;
    }
    return true;
  }

  private readonly copy = (): void => {
    if (!this.term.hasSelection()) return;
    navigator.clipboard.writeText(this.term.getSelection()).then(() => this.events.copied(), () => {});
  };

  // Releasing a drag selection copies it, wherever the pointer is released.
  private readonly startCopy = (): void => window.addEventListener("pointerup", this.copy, { once: true });
}
