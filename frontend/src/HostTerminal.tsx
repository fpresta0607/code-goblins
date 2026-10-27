import { useCallback, useEffect, useRef, useState } from "react";
import "@xterm/xterm/css/xterm.css";
import { message, reportToCfo } from "./api";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { Icon } from "./Icon";
import { putImage } from "./terminal-image";
import { TerminalTextSheet } from "./terminal-text-sheet";
import { TerminalTouchTools } from "./terminal-touch-tools";
import { isDetachedHold } from "./terminalInput";
import { closedReason, ended, reconnects, type ScreenState, storedFontSize, storeFontSize } from "./terminalStream";
import { offerLink } from "./terminal-link";
import { TerminalView } from "./terminalView";
import { useDictation } from "./useDictation";
import { VoiceBubble } from "./VoiceBubble";

// A view that keeps dropping stops retrying and says why.
const MAX_RETRIES = 5;

// A native goblin's terminal: its host's own byte stream drawn by xterm at the
// panel's size. It stays live while the board is open, shown or not, so
// switching to it only brings it into sight. A connection is staged out of
// sight until its screen is whole, and a reconnect keeps the last screen in
// place until the new one is ready, so the panel is never blank, never
// cleared and repainted, and never half drawn.
// query names the terminal to the relay: a task's generation, or the native
// terminal the CFO runs in. A terminal that ended says how in place of
// Reconnect. hasMicrophone is false for a command's terminal on its card,
// which shows the voice bubble only while the dictation keys are held.
// reportAs names the terminal in what it reports to the CFO. link, when set,
// is the terminal deck's key it offers its link by (terminal-link.ts), so the
// Messages view's box types through this terminal's own view. hasComposer says
// the session's message box is the active input surface: its queue note, its
// microphone and its copy control replace this terminal's own, so no second
// layer says the same thing.
export function HostTerminal({ query, harness, label, reportAs, instance, visible, shown, focus, hasMicrophone = true, hasComposer = false, link, onHistory }: { query: string; harness: string; label: string; reportAs: string; instance: string; visible: boolean; shown: boolean; focus: number; hasMicrophone?: boolean; hasComposer?: boolean; link?: string; onHistory?: () => void }) {
  const surface = useRef<HTMLDivElement>(null);
  const current = useRef<TerminalView | null>(null);
  // staged is a connection still replaying out of sight; it is shown and
  // hidden with the panel too, so one begun while the terminal was hidden
  // sizes itself and becomes whole once the terminal is shown.
  const staged = useRef<TerminalView | null>(null);
  const shownValue = useRef(shown);
  const harnessValue = useRef(harness);
  const onHistoryValue = useRef(onHistory);
  const retries = useRef(0);
  const [phase, setPhase] = useState<"connecting" | "live" | "closed">("connecting");
  // Why the view closed, while it is closed, and whether its terminal ended,
  // which no Reconnect brings back.
  const [closedWhy, setClosedWhy] = useState("");
  const [isEnded, setEnded] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [copied, setCopied] = useState(false);
  const [hasScreen, setHasScreen] = useState(false);
  // While he reads the history, where it ends, which Jump to bottom sits
  // above; null at the live end.
  const [historyEnd, setHistoryEnd] = useState<number | null>(null);
  // Whether key presses sit unread in the terminal's input: a busy program
  // takes a key only once it is free, and until then nothing on its screen
  // shows the key was pressed (the Overlord, 2026-10-09, "enter to approve
  // in claude doesnt work").
  const [isUnread, setUnread] = useState(false);
  // Why typing is held while the screen stays live, as while a review gate
  // owns the goblin's work, which says so instead of closing the view
  // (#1304); empty while typing is free.
  const [heldWhy, setHeldWhy] = useState("");
  // Whether a key he typed was not sent, since the view had no connection
  // then, until a new connection is whole.
  const [isUnsent, setUnsent] = useState(false);
  // What the view was last told of the harness's screen.
  const [screen, setScreen] = useState<ScreenState | null>(null);
  // What a touch tool or a pasted picture met, for a moment, and the
  // terminal's text while it is laid out to select.
  const [feedback, showFeedback] = useClickFeedback();
  const dictation = useDictation((text, isSent) => {
    // A live terminal takes what its own microphone heard, and only input it
    // refused as held leaves the words to Speech history for pasting back.
    if (current.current?.held) { showFeedback("Dictation saved to Speech history."); return; }
    current.current?.paste(text);
    if (isSent) current.current?.enter();
  }, instance);
  const dictate = dictation.key;
  const [textSheet, setTextSheet] = useState<string | null>(null);
  // A picture reaches the harness as a desktop paste's does: on this PC's
  // clipboard, then the harness's image key.
  const addImage = useCallback((picture: Blob) => {
    showFeedback("Adding the picture");
    putImage(picture, instance).then(() => { showFeedback(""); current.current?.pressImageKey(); }, (error: unknown) => showFeedback(message(error)));
  }, [instance, showFeedback]);
  const addImageValue = useRef(addImage);
  useEffect(() => { addImageValue.current = addImage; }, [addImage]);
  useEffect(() => {
    shownValue.current = shown;
    for (const view of [current.current, staged.current]) {
      if (!view) continue;
      if (shown) view.setFont(storedFontSize());
      view.show(shown);
    }
  }, [shown]);
  useEffect(() => { harnessValue.current = harness; }, [harness]);
  useEffect(() => { onHistoryValue.current = onHistory; }, [onHistory]);
  const reportAsValue = useRef(reportAs);
  useEffect(() => { reportAsValue.current = reportAs; }, [reportAs]);
  // A switch to this terminal hands it the keyboard, at once or once it is whole.
  const wantFocus = useRef(false);
  useEffect(() => {
    if (!focus) return;
    if (current.current && shownValue.current) current.current.focus(); else wantFocus.current = true;
  }, [focus]);
  useEffect(() => {
    const container = surface.current;
    if (!container || !visible) return;
    let isActive = true;
    let copiedTimer: ReturnType<typeof setTimeout> | undefined, retry: ReturnType<typeof setTimeout> | undefined;
    const url = new URL("/api/terminal/native", location.href);
    url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const params = new URLSearchParams(query);
    params.set("token", instance);
    url.search = params.toString();
    const view: TerminalView = new TerminalView(container, url, storedFontSize(), {
      ready: () => {
        if (!isActive) return;
        const prior = current.current;
        const reading = prior?.readingPosition() ?? null;
        // Disposing the view it replaces takes the keyboard from the page, so
        // where the keyboard was is read first.
        const hadKeyboard = !!prior?.element.contains(document.activeElement);
        current.current = view;
        if (staged.current === view) staged.current = null;
        view.promote();
        view.show(shownValue.current);
        prior?.dispose();
        retries.current = 0;
        setHasScreen(true);
        setHistoryEnd(null);
        setUnread(view.unread);
        setHeldWhy(view.held);
        setScreen(view.screen);
        setUnsent(false);
        setReconnecting(false);
        setClosedWhy("");
        setEnded(false);
        setPhase("live");
        view.restoreReading(reading);
        if (shownValue.current && (wantFocus.current || hadKeyboard)) view.focus();
        wantFocus.current = false;
      },
      closed: (code, why) => {
        if (!isActive) return;
        if (reconnects(code) && retries.current < MAX_RETRIES) {
          retries.current++;
          setReconnecting(true);
          retry = setTimeout(() => setAttempt((prior) => prior + 1), 400 * retries.current);
          return;
        }
        if (why && !isDetachedHold(why)) reportToCfo(reportAsValue.current, closedReason(code, why));
        setReconnecting(false);
        setClosedWhy(closedReason(code, why));
        setEnded(ended(code));
        setPhase("closed");
      },
      copied: () => {
        setCopied(true);
        clearTimeout(copiedTimer);
        copiedTimer = setTimeout(() => setCopied(false), 1400);
      },
      font: storeFontSize,
      dictate,
      harness: () => harnessValue.current,
      image: (picture) => addImageValue.current(picture),
      problem: (why) => showFeedback(why),
      reading: (end) => { if (current.current === view) setHistoryEnd(end); },
      unread: (unread) => { if (current.current === view) setUnread(unread); },
      held: (reason) => { if (current.current === view) setHeldWhy(reason); },
      unsent: () => { if (current.current === view) setUnsent(true); },
      screen: (state) => { if (current.current === view) setScreen(state); },
      history: () => { if (current.current === view) onHistoryValue.current?.(); },
    });
    staged.current = view;
    view.show(shownValue.current);
    return () => {
      isActive = false;
      clearTimeout(copiedTimer);
      clearTimeout(retry);
      if (staged.current === view) staged.current = null;
      // A view that is on screen stays until its replacement is whole.
      if (current.current !== view) view.dispose();
    };
  }, [query, instance, visible, attempt, dictate, showFeedback]);
  useEffect(() => () => { current.current?.dispose(); current.current = null; }, []);
  useEffect(() => {
    if (!link) return;
    offerLink(link, {
      phase, isReconnecting: reconnecting || !visible, isDictating: dictation.listening, isUnread, held: heldWhy, screen,
      stopDictation: dictation.stop,
      feedback: feedback || dictation.note,
      send: (text) => phase === "live" && !reconnecting && visible ? current.current?.submit(text) ?? null : null,
      paste: (text) => phase === "live" && !reconnecting && visible && !heldWhy ? current.current?.paste(text) ?? false : false,
      escape: () => !!current.current?.escape(),
      control: (keys) => phase === "live" && !reconnecting && visible ? current.current?.control(keys) ?? false : false,
    });
    return () => offerLink(link, null);
  }, [link, phase, reconnecting, visible, isUnread, heldWhy, screen, dictation.listening, dictation.stop, dictation.note, feedback]);
  return <section className={"native-terminal host-terminal" + (hasComposer ? " has-composer" : "")} aria-label={label}>
    <div className="terminal-surface" ref={surface} />
    {/* Connecting is a plain line under the terminal's box, never a cover over
        it: he types into that box while it connects, and the loader must sit
        outside it (the Overlord, 2026-10-27: "the connecting loader overlays
        and looks improper on box"). */}
    {phase === "connecting" && <p className="terminal-connecting" role="status"><span className="terminal-spinner" aria-hidden="true" />Connecting to the terminal</p>}
    {phase === "live" && (reconnecting || !visible) && <span className="terminal-state terminal-reconnecting" role="status"><span className="status-dot" />Reconnecting</span>}
    {phase === "closed" && <div className={hasScreen ? "terminal-closed" : "terminal-cover"} role="status"><Icon name="terminal" />{closedWhy && <p>{closedWhy}</p>}{!isEnded && <button className="primary" disabled={!visible} onClick={() => { retries.current = 0; setUnsent(false); setPhase(current.current ? "live" : "connecting"); setReconnecting(!!current.current); setAttempt((prior) => prior + 1); }}>Reconnect</button>}</div>}
    {copied && <span className="terminal-state terminal-copied" role="status">Copied</span>}
    {phase !== "connecting" && isUnsent && <span className="terminal-state terminal-unsent" role="status"><span className="status-dot" />{phase === "live" ? "Not sent. The terminal is reconnecting." : "Not sent. The terminal is closed."}</span>}
    {phase === "live" && heldWhy && !hasComposer && <p className="terminal-held-line" role="status">Typing is paused.</p>}
    {phase === "live" && isUnread && !hasComposer && !isUnsent && !heldWhy && <span className="terminal-state terminal-unread" role="status" data-tip="The program is busy. It takes what was typed when it is free, so there is no need to press again."><span className="status-dot" />Program busy. Typed keys are waiting.</span>}
    {phase === "live" && historyEnd !== null && <button type="button" className="icon-button raised terminal-jump" aria-label="Jump to bottom" data-tip="Jump to bottom" style={{ top: historyEnd - 8 }} onClick={() => current.current?.jumpToBottom()}><Icon name="arrow-down" /></button>}
    {textSheet !== null && <TerminalTextSheet text={textSheet} onClose={() => { setTextSheet(null); current.current?.focus(); }} />}
    {hasComposer && <button type="button" className="icon-button terminal-copy" aria-label="Copy terminal text" data-tip="Copy terminal text" onClick={() => setTextSheet(current.current?.text() ?? "")}><Icon name="copy" /></button>}
    {(hasMicrophone || dictation.listening) && <VoiceBubble listening={dictation.listening} level={dictation.level} model={dictation.model} onTap={dictation.toggle} onPaste={(text) => { current.current?.paste(text); current.current?.focus(); }}
      tools={<TerminalTouchTools events={{ text: (text) => current.current?.paste(text), image: addImage, copy: () => setTextSheet(current.current?.text() ?? ""), problem: showFeedback }} />} />}
    {!hasComposer && (feedback ? <p className="terminal-note"><ClickFeedback text={feedback} /></p> : dictation.note && <p className="terminal-note" role="status">{dictation.note}</p>)}
  </section>;
}
