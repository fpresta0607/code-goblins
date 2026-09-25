import { useEffect, useRef, useState } from "react";
import "@xterm/xterm/css/xterm.css";
import type { Task } from "./types";
import { Icon } from "./Icon";
import { closedReason, DEFAULT_FONT_SIZE, MAX_FONT_SIZE, MIN_FONT_SIZE, reconnects } from "./terminalStream";
import { TerminalView } from "./terminalView";

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
// panel's size. It stays live while the board is open, shown or not, so
// switching to it only brings it into sight. A connection is staged out of
// sight until its screen is whole, and a reconnect keeps the last screen in
// place until the new one is ready, so the panel is never blank, never
// cleared and repainted, and never half drawn.
export function HostTerminal({ task, instance, visible, shown, focus }: { task: Task; instance: string; visible: boolean; shown: boolean; focus: number }) {
  const surface = useRef<HTMLDivElement>(null);
  const current = useRef<TerminalView | null>(null);
  const shownValue = useRef(shown);
  const retries = useRef(0);
  const [phase, setPhase] = useState<"connecting" | "live" | "closed">("connecting");
  const [reconnecting, setReconnecting] = useState(false);
  const [reason, setReason] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [copied, setCopied] = useState(false);
  const [hasScreen, setHasScreen] = useState(false);
  useEffect(() => {
    shownValue.current = shown;
    const view = current.current;
    if (!view) return;
    if (shown) view.setFont(storedFontSize());
    view.show(shown);
  }, [shown]);
  // A switch to this terminal hands it the keyboard, at once or once it is whole.
  const wantFocus = useRef(false);
  useEffect(() => {
    if (!focus) return;
    if (current.current && shownValue.current) current.current.focus(); else wantFocus.current = true;
  }, [focus]);
  useEffect(() => {
    const container = surface.current;
    if (!container || !visible) return;
    let copiedTimer: ReturnType<typeof setTimeout> | undefined, retry: ReturnType<typeof setTimeout> | undefined;
    const url = new URL("/api/terminal/native", location.href);
    url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
    url.search = new URLSearchParams({ task: task.id, generation: task.generation, token: instance }).toString();
    const view: TerminalView = new TerminalView(container, url, storedFontSize(), {
      ready: () => {
        const prior = current.current;
        current.current = view;
        view.promote();
        view.show(shownValue.current);
        prior?.dispose();
        retries.current = 0;
        setHasScreen(true);
        setReconnecting(false);
        setPhase("live");
        if (shownValue.current && (wantFocus.current || container.closest(".context-pane")?.contains(document.activeElement))) view.focus();
        wantFocus.current = false;
      },
      closed: (code, why) => {
        if (reconnects(code) && retries.current < MAX_RETRIES) {
          retries.current++;
          setReconnecting(true);
          retry = setTimeout(() => setAttempt((prior) => prior + 1), 400 * retries.current);
          return;
        }
        setReason(closedReason(code, why));
        setReconnecting(false);
        setPhase("closed");
      },
      copied: () => {
        setCopied(true);
        clearTimeout(copiedTimer);
        copiedTimer = setTimeout(() => setCopied(false), 1400);
      },
      font: (size) => { try { localStorage.setItem(FONT_KEY, String(size)); } catch { /* the size still applies to this view */ } },
    });
    view.show(shownValue.current);
    return () => {
      clearTimeout(copiedTimer);
      clearTimeout(retry);
      // A view that is on screen stays until its replacement is whole.
      if (current.current !== view) view.dispose();
    };
  }, [task.id, task.generation, instance, visible, attempt]);
  useEffect(() => () => { current.current?.dispose(); current.current = null; }, []);
  return <section className="native-terminal host-terminal" aria-label="Goblin terminal">
    <div className="terminal-surface" ref={surface} />
    {phase === "connecting" && <div className="terminal-cover" role="status"><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the terminal</p></div>}
    {phase === "live" && (reconnecting || !visible) && <span className="terminal-state terminal-reconnecting" role="status"><span className="status-dot" />Reconnecting</span>}
    {phase === "closed" && <div className={hasScreen ? "terminal-closed" : "terminal-cover"} role="status"><Icon name="terminal" /><p>{reason}</p><button className="primary" disabled={!visible} onClick={() => { retries.current = 0; setPhase(current.current ? "live" : "connecting"); setReconnecting(!!current.current); setAttempt((prior) => prior + 1); }}>Reconnect</button></div>}
    {copied && <span className="terminal-state terminal-copied" role="status">Copied</span>}
  </section>;
}
