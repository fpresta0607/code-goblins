import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import type { Task } from "./types";
import { Icon } from "./Icon";
import { terminalDocument } from "./terminalDocument";
import { ackDue, closedReason, DEFAULT_FONT_SIZE, fontSizeFor, inputMessages, MAX_FONT_SIZE, MIN_FONT_SIZE, parseSize, reconnects, usableSize } from "./terminalStream";

const FALLBACK_FONT = '"Cascadia Mono", Consolas, monospace';
const FONT_KEY = "cfo-terminal-font-size";
// A view that keeps dropping stops retrying and says why.
const MAX_RETRIES = 5;

function storedFontSize(): number {
  try {
    const size = Number(localStorage.getItem(FONT_KEY));
    return size >= MIN_FONT_SIZE && size <= MAX_FONT_SIZE ? size : DEFAULT_FONT_SIZE;
  } catch { return DEFAULT_FONT_SIZE; }
}

// A native goblin's terminal: its host's own byte stream drawn by xterm at the
// panel's size. The view replays the host's history, then sends its size,
// which makes the pseudo console repaint its whole window, so the screen is
// whole from the first frame. Keys go straight over the open socket, the
// terminal owns its scrollback, and output is acknowledged as xterm consumes
// it. The last view to attach or type sizes the terminal; the others draw at
// that size until they are typed into.
export function HostTerminal({ task, instance, visible, shown }: { task: Task; instance: string; visible: boolean; shown: boolean }) {
  const surface = useRef<HTMLDivElement>(null);
  const [phase, setPhase] = useState<"connecting" | "live" | "closed">("connecting");
  const [reason, setReason] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [copied, setCopied] = useState(false);
  const retries = useRef(0);
  const shownValue = useRef(shown);
  const claimSize = useRef<() => void>(() => {});
  useEffect(() => { shownValue.current = shown; if (shown) claimSize.current(); }, [shown]);
  useEffect(() => {
    const element = surface.current;
    if (!element || !visible) return;
    let disposed = false;
    let retry: ReturnType<typeof setTimeout> | undefined, copiedTimer: ReturnType<typeof setTimeout> | undefined, frame = 0;
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || "";
    const term = new Terminal({ documentOverride: terminalDocument(nonce), fontSize: storedFontSize(), fontFamily: FALLBACK_FONT, lineHeight: 1.2, scrollback: 5000, cursorBlink: false, theme: { background: "#071015", foreground: "#d8e9e2", cursor: "#6ee7b7", selectionBackground: "#286856" }, linkHandler: { activate: () => {} } });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(element);
    term.textarea?.setAttribute("aria-label", "Terminal input");
    term.parser.registerOscHandler(52, () => true);
    const url = new URL("/api/terminal/native", location.href);
    url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
    url.search = new URLSearchParams({ task: task.id, generation: task.generation, token: instance }).toString();
    const socket = new WebSocket(url);
    socket.binaryType = "arraybuffer";
    const encoder = new TextEncoder();
    let written = 0, consumed = 0, acknowledged = 0, owner = true, repainted = false, painted = false;
    const send = (data: string | Uint8Array) => { if (socket.readyState === WebSocket.OPEN) socket.send(data); };
    // Sizing the terminal to this panel makes this view its owner; the first
    // size it sends repaints the screen even when it matches.
    const claim = () => {
      if (!shownValue.current || socket.readyState !== WebSocket.OPEN) return;
      const size = fit.proposeDimensions();
      if (!size || !usableSize(size.cols, size.rows)) return;
      if (repainted && owner && size.cols === term.cols && size.rows === term.rows) return;
      owner = true;
      repainted = true;
      if (size.cols !== term.cols || size.rows !== term.rows) term.resize(size.cols, size.rows);
      send(JSON.stringify({ type: "resize", cols: size.cols, rows: size.rows }));
    };
    claimSize.current = claim;
    const refit = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(claim); };
    void document.fonts.load('16px "JetBrains Mono"').then(() => {
      if (!disposed && document.fonts.check('16px "JetBrains Mono"')) { term.options.fontFamily = '"JetBrains Mono", ' + FALLBACK_FONT; refit(); }
    }, () => {});
    socket.onopen = claim;
    socket.onmessage = (event: MessageEvent<ArrayBuffer | string>) => {
      if (typeof event.data === "string") {
        // Another view sized the terminal: draw at its size until typed into.
        const size = parseSize(event.data);
        if (size && (size.cols !== term.cols || size.rows !== term.rows)) { owner = false; term.resize(size.cols, size.rows); }
        return;
      }
      const bytes = new Uint8Array(event.data);
      written += bytes.length;
      term.write(bytes, () => {
        consumed += bytes.length;
        if (ackDue(consumed, acknowledged, written)) { acknowledged = consumed; send(JSON.stringify({ type: "ack", bytes: consumed })); }
        if (painted || disposed) return;
        painted = true;
        retries.current = 0;
        setPhase("live");
        if (shownValue.current && element.closest(".context-pane")?.contains(document.activeElement)) term.focus();
      });
    };
    socket.onclose = (event) => {
      if (disposed) return;
      term.options.disableStdin = true;
      if (reconnects(event.code) && retries.current < MAX_RETRIES) {
        retries.current++;
        setPhase("connecting");
        retry = setTimeout(() => setAttempt((prior) => prior + 1), 400 * retries.current);
        return;
      }
      setReason(closedReason(event.code, event.reason));
      setPhase("closed");
    };
    term.onData((data) => {
      if (!owner) claim();
      for (const piece of inputMessages(encoder.encode(data))) send(piece);
    });
    term.onBinary((data) => send(Uint8Array.from(data, (character) => character.charCodeAt(0) & 255)));
    const copy = () => {
      if (!term.hasSelection()) return;
      navigator.clipboard.writeText(term.getSelection()).then(() => {
        setCopied(true);
        clearTimeout(copiedTimer);
        copiedTimer = setTimeout(() => setCopied(false), 1400);
      }, () => {});
    };
    // Releasing a drag selection copies it, wherever the pointer is released.
    const startCopy = () => window.addEventListener("pointerup", copy, { once: true });
    element.addEventListener("pointerdown", startCopy);
    term.attachCustomKeyEventHandler((event) => {
      // Escape and every other key belong to the terminal, never the panel.
      event.stopPropagation();
      const down = event.type === "keydown";
      if (event.shiftKey && event.key === "Escape") {
        event.preventDefault();
        if (down) element.closest(".goblin-panel")?.querySelector<HTMLButtonElement>(".panel-pill button[aria-pressed='true']")?.focus();
        return false;
      }
      if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "c") {
        event.preventDefault();
        if (down) copy();
        return false;
      }
      const size = event.ctrlKey && !event.altKey && !event.metaKey ? fontSizeFor(event.key, term.options.fontSize ?? DEFAULT_FONT_SIZE) : null;
      if (size !== null) {
        event.preventDefault();
        if (down) {
          term.options.fontSize = size;
          try { localStorage.setItem(FONT_KEY, String(size)); } catch { /* the size still applies to this view */ }
          refit();
        }
        return false;
      }
      return true;
    });
    const resize = new ResizeObserver(refit);
    resize.observe(element);
    return () => {
      disposed = true;
      clearTimeout(retry);
      clearTimeout(copiedTimer);
      cancelAnimationFrame(frame);
      claimSize.current = () => {};
      resize.disconnect();
      element.removeEventListener("pointerdown", startCopy);
      window.removeEventListener("pointerup", copy);
      socket.onclose = null;
      socket.close(1000);
      term.dispose();
    };
  }, [task.id, task.generation, instance, visible, attempt]);
  return <section className="native-terminal host-terminal" aria-label="Goblin terminal">
    <div className="terminal-surface" ref={surface} />
    {phase !== "live" && <div className="terminal-cover" role="status">
      {phase === "connecting"
        ? <><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the terminal</p></>
        : <><Icon name="terminal" /><p>{reason}</p><button className="primary" disabled={!visible} onClick={() => { retries.current = 0; setPhase("connecting"); setAttempt((prior) => prior + 1); }}>Reconnect</button></>}
    </div>}
    {copied && <span className="terminal-state terminal-copied" role="status">Copied</span>}
  </section>;
}
