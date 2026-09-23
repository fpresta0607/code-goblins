import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { message, request } from "./api";
import { object, string, type Session, type Task } from "./types";
import { Avatar } from "./Avatar";
import { personaFor } from "./workflow";
import { sessionTitle, ownsTaskSession } from "./lineageTree";
import { WorkspaceDetails } from "./WorkspaceDetails";
import { bracketedPaste, inputBytes, maxInputBytes, queueInput, type TerminalCommand } from "./terminalInput";
import { terminalDocument } from "./terminalDocument";

interface Connection { identity: string; lease: string; control: boolean }

export function NativeTerminal({ task, node, instance, visible, onOwner }: { task?: Task; node?: Session; instance: string; visible: boolean; onOwner?: () => void }) {
  const host = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const inputControl = useRef<HTMLButtonElement>(null);
  const [readerSupport, setReaderSupport] = useState(() => { try { return localStorage.getItem("cfo-terminal-screen-reader") === "true"; } catch { return false; } });
  const [preferenceError, setPreferenceError] = useState("");
  const readerValue = useRef(readerSupport);
  useEffect(() => { readerValue.current = readerSupport; if (terminal.current) terminal.current.options.screenReaderMode = readerSupport; }, [readerSupport]);
  const usedControlAttempt = useRef(-1);
  const [connection, setConnection] = useState<Connection | null>(null);
  const [status, setStatus] = useState("Connecting to native terminal...");
  const [error, setError] = useState("");
  const [unavailable, setUnavailable] = useState(false);
  const [attempt, setAttempt] = useState({ version: 0, control: false, identity: "" });
  const taskID = task?.id || "", generation = task?.generation || "", session = node?.id || "";
  const cfo = !task && !node;
  const shared = !!node && !ownsTaskSession(node, task);
  const queued = !!task && !task.generation;
  const missing = !cfo && (shared || queued) || unavailable;
  useEffect(() => {
    if (!host.current || !visible || missing) return;
    const element = host.current;
    const abort = new AbortController();
    const wantsControl = attempt.control && usedControlAttempt.current !== attempt.version;
    if (wantsControl) usedControlAttempt.current = attempt.version;
    const nonce = document.querySelector<HTMLMetaElement>('meta[name="cfo-style-nonce"]')?.content || "";
    const term = new Terminal({ documentOverride: terminalDocument(nonce), fontSize: 15, fontFamily: '"Cascadia Code", Consolas, monospace', lineHeight: 1.2, scrollback: 0, disableStdin: true, cursorBlink: false, screenReaderMode: readerValue.current, theme: { background: "#071015", foreground: "#d8e9e2", cursor: "#6ee7b7", selectionBackground: "#286856" }, linkHandler: { activate: () => {} } });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(element);
    terminal.current = term;
    term.textarea?.setAttribute("aria-label", "Native terminal keyboard input");
    term.parser.registerOscHandler(52, () => true);
    let active: Connection | null = null;
    let seq = 0, pendingBytes = 0, frameSeq = 0;
    let full = false, flushing = false;
    const queue: TerminalCommand[] = [];
    let inputTimer: ReturnType<typeof setTimeout> | undefined;
    let resizeTimer: ReturnType<typeof setTimeout>;
    let lastDimensions = "";
    const dimensions = () => {
      const size = fit.proposeDimensions();
      return { cols: Math.min(400, Math.max(20, size?.cols || 80)), rows: Math.min(160, Math.max(5, size?.rows || 24)) };
    };
    const stop = (reason: string) => {
      active = null;
      term.options.disableStdin = true;
      abort.abort();
      setConnection(null);
      setStatus("Disconnected");
      setError(reason);
    };
    const flush = async () => {
      inputTimer = undefined;
      flushing = true;
      while (queue.length && active?.control && !abort.signal.aborted) {
        const command = queue.shift()!;
        const size = command.text ? inputBytes(command.text) : 1;
        const lease = active.lease;
        try {
          await request("/api/terminal/input", abort.signal, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ lease, seq: ++seq, command }) });
          pendingBytes -= size;
        } catch (e: unknown) { if (!abort.signal.aborted) stop(message(e) + " Input was not retried."); break; }
      }
      flushing = false;
    };
    const send = (command: TerminalCommand) => {
      if (!active?.control || abort.signal.aborted) return;
      const size = command.text ? inputBytes(command.text) : 1;
      if (size > maxInputBytes) { setError("Input exceeds 64 KiB. Use a smaller selection; nothing was sent."); return; }
      if (command.type === "terminal.input") setError("");
      pendingBytes += size;
      if (pendingBytes > maxInputBytes || queue.length >= 256) { stop("Input queue is full. Unsent input was discarded; reconnect deliberately."); return; }
      queueInput(queue, command);
      if (!flushing && !inputTimer) inputTimer = setTimeout(() => { void flush(); }, 20);
    };
    term.onData((text) => send({ type: "terminal.input", text }));
    const paste = (event: ClipboardEvent) => {
      event.preventDefault(); event.stopImmediatePropagation();
      const text = event.clipboardData?.getData("text/plain");
      if (text) { try { send({ type: "terminal.input", text: bracketedPaste(text) }); } catch (e: unknown) { setError(message(e)); } }
    };
    element.addEventListener("paste", paste, true);
    term.attachCustomKeyEventHandler((event) => {
      // Escape belongs to the connected terminal, never the surrounding pane.
      event.stopPropagation();
      if (event.shiftKey && event.key === "Escape") {
        event.preventDefault();
        if (event.type === "keydown") { active = null; abort.abort(); term.options.disableStdin = true; setAttempt((prior) => ({ version: prior.version + 1, control: false, identity: "" })); inputControl.current?.focus(); }
        return false;
      }
      if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === "c") {
        event.preventDefault();
        if (event.type === "keydown" && term.hasSelection()) void navigator.clipboard.writeText(term.getSelection()).catch(() => setError("Clipboard access was refused. Use the browser's copy command on selected text."));
        return false;
      }
      if (event.key === "PageUp" || event.key === "PageDown") {
        event.preventDefault();
        if (event.type === "keydown") send({ type: "terminal.scroll", direction: event.key === "PageUp" ? "up" : "down", lines: term.rows, source: "page_key" });
        return false;
      }
      return !!active?.control;
    });
    term.attachCustomWheelEventHandler((event) => {
      event.preventDefault();
      if (event.deltaY) send({ type: "terminal.scroll", direction: event.deltaY < 0 ? "up" : "down", lines: 3, source: "wheel" });
      return false;
    });
    const resize = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        const size = dimensions(), key = JSON.stringify(size);
        if (active?.control && key !== lastDimensions) { lastDimensions = key; send({ type: "terminal.resize", ...size }); }
      }, 200);
    });
    resize.observe(element);
    const read = async () => {
      setConnection(null); setError(""); setStatus("Connecting to native terminal...");
      try {
        const size = dimensions();
        lastDimensions = JSON.stringify(size);
        const response = await fetch("/api/terminal/stream", { signal: abort.signal, method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ task: taskID, session, generation, ...size, control: wantsControl, identity: attempt.identity }) });
        if (!response.ok) {
          const failure = object(await response.json());
          if (failure.code === "terminal_unavailable") setUnavailable(true);
          throw new Error(string(failure.error));
        }
        if (!response.body) throw new Error("Native stream is unavailable.");
        const reader = response.body.getReader(), decoder = new TextDecoder();
        let buffer = "";
        while (!abort.signal.aborted) {
          const next = await reader.read();
          if (next.done) throw new Error("Native connection ended. Reconnect for a fresh screen.");
          buffer += decoder.decode(next.value, { stream: true });
          if (buffer.length > 4 * 1024 * 1024) throw new Error("Native frame exceeds its limit.");
          let end: number;
          while ((end = buffer.indexOf("\n")) >= 0) {
            const frame = object(JSON.parse(buffer.slice(0, end))); buffer = buffer.slice(end + 1);
            if (frame.type === "terminal.closed") throw new Error(string(frame.reason));
            if (frame.type === "terminal.ready") {
              active = { identity: string(frame.identity), lease: string(frame.lease), control: frame.control === true };
              continue;
            }
            if (frame.type !== "terminal.frame") continue;
            if (!active || frame.encoding !== "ansi" || typeof frame.seq !== "number" || (!full && frame.full !== true) || (full && frame.full !== true && frame.seq !== frameSeq + 1) || typeof frame.width !== "number" || typeof frame.height !== "number") throw new Error("Native screen synchronization lost. Reconnect for a full frame.");
            frameSeq = frame.seq;
            if (frame.full === true) term.reset();
            term.resize(frame.width, frame.height);
            const bytes = Uint8Array.from(atob(string(frame.bytes)), (character) => character.charCodeAt(0));
            await new Promise<void>((resolve) => term.write(bytes, resolve));
            if (!full) { full = true; term.options.disableStdin = !active.control; setConnection(active); setStatus(active.control ? "Input connected" : "Live native screen · Read only"); }
          }
        }
      } catch (e: unknown) { if (!abort.signal.aborted) stop(message(e)); }
    };
    void read();
    return () => { active = null; abort.abort(); queue.length = 0; clearTimeout(inputTimer); clearTimeout(resizeTimer); resize.disconnect(); element.removeEventListener("paste", paste, true); term.dispose(); terminal.current = null; };
  }, [taskID, generation, session, instance, visible, attempt, missing]);
  return <section className="native-terminal-pane" aria-label={cfo ? "CFO terminal" : "Selected native terminal"}>
    <header className="panel-header"><Avatar persona={cfo ? "cfo" : personaFor(task, node)} small /><div><h2>{cfo ? "CFO" : node ? sessionTitle(node, task) : task?.title}</h2>{task?.project && <p className="project-label">{task.project}</p>}</div>
      <details className="terminal-workspace"><summary>Workspace details</summary><WorkspaceDetails task={task} node={node} instance={instance} /></details>
    </header>
    {missing ? <div className="terminal-empty"><p>{queued ? "This task has not started yet." : shared ? "This child has no separate terminal." : error}</p>{onOwner && shared && <button onClick={onOwner}>Open owning task</button>}</div> : <>
    <div className="terminal-toolbar"><span role="status">{status}</span>
      {connection ? <button ref={inputControl} disabled={!visible} onClick={() => setAttempt((prior) => ({ version: prior.version + 1, control: !connection.control, identity: connection.identity }))}>{connection.control ? "Release input" : "Connect input"}</button>
        : <button ref={inputControl} disabled={!visible} onClick={() => setAttempt((prior) => ({ version: prior.version + 1, control: false, identity: "" }))}>Reconnect</button>}
    </div>
    {error && <p className="error-box" role="alert">{error}</p>}
    <div className="terminal-surface" ref={host} />
    <p className="terminal-caption">{connection?.control ? "Ctrl+C interrupts. Shift+Escape releases input. Ctrl+Shift+C copies a selection." : "Connect input to type, paste or use native scrollback. No session is started."}</p>
    <details className="terminal-options"><summary>Terminal options</summary><label><input type="checkbox" checked={readerSupport} onChange={(event) => {
      const enabled = event.target.checked; setReaderSupport(enabled); setPreferenceError("");
      try { localStorage.setItem("cfo-terminal-screen-reader", String(enabled)); } catch { setPreferenceError("This preference could not be saved in this browser."); }
    }} />Screen reader support</label><p>Enables accessible terminal output. Some text input methods are unavailable in this mode; paste remains supported.</p>{preferenceError && <p role="status">{preferenceError}</p>}</details></>}
  </section>;
}
