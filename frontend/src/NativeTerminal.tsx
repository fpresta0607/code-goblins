import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { message, request } from "./api";
import { object, string, type Session, type Task } from "./types";
import { ownsTaskSession } from "./lineageTree";
import { Icon } from "./Icon";
import { bracketedPaste, fittedFontSize, inputBytes, maxInputBytes, queueInput, typingHeldReason } from "./terminalInput";
import { terminalDocument } from "./terminalDocument";
import { useDictation } from "./useDictation";

const FALLBACK_FONT = '"Cascadia Mono", Consolas, monospace';

// The goblin's live Herdr pane cast into the board: a view stream whose frames
// arrive at the pane's own size, fitted to the panel's width, and whose lease
// takes typing straight away. Nothing here resizes or scrolls the real pane.
// An input the supervisor refuses, or whose outcome is unknown, ends the view;
// it is never resent, and reconnecting starts from a fresh full screen.
export function NativeTerminal({ task, node, instance, visible, shown, focus = 0, onOwner }: { task?: Task; node?: Session; instance: string; visible: boolean; shown: boolean; focus?: number; onOwner?: () => void }) {
  const host = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const shownValue = useRef(shown);
  useEffect(() => { shownValue.current = shown; }, [shown]);
  // A switch to this terminal hands it the keyboard, at once or on its first
  // frame.
  const wantFocus = useRef(false);
  const liveValue = useRef(false);
  useEffect(() => {
    if (!focus) return;
    if (terminal.current && liveValue.current && shownValue.current) terminal.current.focus(); else wantFocus.current = true;
  }, [focus]);
  const [live, setLive] = useState(false);
  const [status, setStatus] = useState("Connecting");
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
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
    if (!host.current || !visible || missing) return;
    const element = host.current;
    const abort = new AbortController();
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || "";
    const term = new Terminal({ documentOverride: terminalDocument(nonce), fontSize: 15, fontFamily: FALLBACK_FONT, lineHeight: 1.2, scrollback: 0, disableStdin: true, cursorBlink: false, theme: { background: "#071015", foreground: "#d8e9e2", cursor: "#6ee7b7", selectionBackground: "#286856" }, linkHandler: { activate: () => {} } });
    term.open(element);
    terminal.current = term;
    // The bundled face measures differently from the fallback, so switch once
    // it has loaded; the changed option makes xterm measure its cells again.
    void document.fonts.load('15px "JetBrains Mono"').then(() => {
      if (!abort.signal.aborted && document.fonts.check('15px "JetBrains Mono"')) term.options.fontFamily = '"JetBrains Mono", ' + FALLBACK_FONT;
    }, () => {});
    term.textarea?.setAttribute("aria-label", "Terminal input");
    term.parser.registerOscHandler(52, () => true);
    let lease = "", seq = 0, frameSeq = 0, full = false, flushing = false;
    const queue: string[] = [];
    let copiedTimer: ReturnType<typeof setTimeout> | undefined;
    const fit = () => {
      const size = fittedFontSize(element.clientWidth, term.cols);
      if (term.options.fontSize !== size) term.options.fontSize = size;
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
    // Inputs go one at a time in order, each with the lease's next number.
    const flush = async () => {
      if (flushing) return;
      flushing = true;
      while (queue.length && lease && !abort.signal.aborted) {
        const text = queue.shift()!;
        try {
          await request("/api/terminal/input", abort.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ lease, seq: ++seq, command: { type: "terminal.input", text } }) });
        } catch (e: unknown) {
          if (!abort.signal.aborted) stop(message(e));
          break;
        }
      }
      flushing = false;
    };
    const send = (raw: string) => {
      // Ctrl+Space types a NUL, which a view refuses; drop it here instead.
      const text = raw.replaceAll("\x00", "");
      if (!text || !lease || abort.signal.aborted) return;
      if (inputBytes(text) > maxInputBytes) { setError("Input exceeds 64 KiB. Use a smaller selection; nothing was sent."); return; }
      setError("");
      queueInput(queue, text);
      void flush();
    };
    term.onData(send);
    const typePaste = (text: string) => { try { send(bracketedPaste(text)); } catch (e: unknown) { setError(message(e)); } };
    pasteText.current = typePaste;
    const paste = (event: ClipboardEvent) => {
      event.preventDefault(); event.stopImmediatePropagation();
      const text = event.clipboardData?.getData("text/plain");
      if (text) typePaste(text);
    };
    element.addEventListener("paste", paste, true);
    const copy = () => {
      if (!term.hasSelection()) return;
      navigator.clipboard.writeText(term.getSelection()).then(() => {
        setCopied(true);
        clearTimeout(copiedTimer);
        copiedTimer = setTimeout(() => setCopied(false), 1400);
      }, () => setError("Clipboard access was refused. Use the browser's copy command on selected text."));
    };
    // Releasing a drag selection copies it, the way Herdr does, wherever the
    // pointer is released.
    const startCopy = () => window.addEventListener("pointerup", copy, { once: true });
    element.addEventListener("pointerdown", startCopy);
    term.attachCustomKeyEventHandler((event) => {
      // Escape belongs to the pane, never the surrounding panel.
      event.stopPropagation();
      const dictated = dictate(event);
      if (dictated !== null) return dictated;
      if (event.shiftKey && event.key === "Escape") {
        event.preventDefault();
        if (event.type === "keydown") element.closest(".context-pane")?.querySelector<HTMLButtonElement>(".panel-pill button[aria-pressed='true']")?.focus();
        return false;
      }
      if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "c") {
        event.preventDefault();
        if (event.type === "keydown") copy();
        return false;
      }
      return !!lease;
    });
    // The wheel scrolls the panel when the pane is taller than it; a view
    // never scrolls the real pane.
    term.attachCustomWheelEventHandler(() => false);
    const resize = new ResizeObserver(fit);
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
            if (term.cols !== frame.width || term.rows !== frame.height) { term.resize(frame.width, frame.height); fit(); }
            const bytes = Uint8Array.from(atob(string(frame.bytes)), (character) => character.charCodeAt(0));
            await new Promise<void>((resolve) => term.write(bytes, resolve));
            if (!full) {
              full = true;
              term.options.disableStdin = false;
              liveValue.current = true;
              setLive(true);
              setStatus("Live");
              if (shownValue.current && (wantFocus.current || element.closest(".context-pane")?.contains(document.activeElement))) term.focus();
              wantFocus.current = false;
            }
          }
        }
      } catch (e: unknown) { if (!abort.signal.aborted) stop(message(e)); }
    };
    void read();
    return () => { lease = ""; liveValue.current = false; abort.abort(); queue.length = 0; clearTimeout(copiedTimer); resize.disconnect(); element.removeEventListener("paste", paste, true); element.removeEventListener("pointerdown", startCopy); window.removeEventListener("pointerup", copy); term.dispose(); terminal.current = null; pasteText.current = null; };
  }, [taskID, generation, session, instance, visible, attempt, missing, dictate]);
  if (missing) return <div className="terminal-empty"><Icon name="terminal" /><p>{queued ? "This task has not started yet." : shared ? "This child has no separate terminal." : error}</p>{onOwner && shared && <button className="primary" onClick={onOwner}>Open owning task</button>}</div>;
  return <section className="native-terminal" aria-label={cfo ? "CFO terminal" : "Goblin terminal"}>
    <div className="terminal-surface" ref={host} />
    {!live && status === "Connecting" && <div className="terminal-cover" role="status"><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the terminal</p></div>}
    <div className="terminal-overlay">
      {/* A live pane shows nothing over the screen; only a change of state or
          a copy needs saying, and the cover says it is connecting. */}
      {(live || status !== "Connecting") && <span className={"terminal-state" + (live ? " live" : "") + (live && !copied ? " quiet" : "")} role="status">
        <span className="status-dot" />{copied ? "Copied" : status}
      </span>}
      {!live && status !== "Connecting" && <button className="icon-button raised" disabled={!visible} aria-label="Reconnect" data-tip="Reconnect" data-tip-align="end" onClick={() => setAttempt((prior) => prior + 1)}><Icon name="refresh" /></button>}
    </div>
    {dictation.listening && <span className="terminal-state live terminal-listening" role="status"><Icon name="mic" />Listening</span>}
    {error ? <p className="terminal-error" role="alert">{error}</p> : dictation.note && <p className="terminal-error" role="status">{dictation.note}</p>}
  </section>;
}
