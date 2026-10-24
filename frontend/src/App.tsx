import { Suspense, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { useRuntimeStream } from "./stream";
import { useItemState } from "./use-item-state";
import { useTaskClicks } from "./use-task-clicks";
import { withClicks } from "./task-clicks";
import { childSelection, Lineage, type Selection } from "./Lineage";
import { Board, type BoardLayout } from "./Board";
import { Orchestration } from "./Orchestration";
import { CommandCenter, type CommandFocus } from "./CommandCenter";
import { ComebackBanner } from "./ComebackBanner";
import { useActivity } from "./useActivity";
import { livePresentations } from "./activity";
import { useReview } from "./review";
import { ownsTaskSession } from "./lineageTree";
import { Icon } from "./Icon";
import { Avatar } from "./Avatar";
import { GoblinPanel, type PanelView } from "./GoblinPanel";
import { PaneDivider } from "./PaneDivider";
import { PanelStage } from "./panel-stage";
import { PANEL_IMPORTANCE, PanelRow, type PanelControl } from "./panel-row";
import { CFO_KEY, MAXIMIZED_KEYS, firstOpen, maximizedFor, maximizedView, paneTrack, switchOrder } from "./terminalOrder";
import { useSwitchKeys } from "./useSwitchKeys";
import { missingPageAction, unsentComment, updateAction } from "./boardUpdate";
import { boardPage, usePageMissing } from "./board-page";
import { useWindowMove } from "./useWindowMove";
import { useAppBadgeRenewal } from "./use-app-badge-renewal";
import { updateSucceededRecently } from "./update-progress";
import { windowTarget } from "./terminalWindow";
import { message, request } from "./api";
import { FirstRun } from "./FirstRun";
import { Alerts } from "./Alerts";
import { AfkBoard } from "./afk-board";
import { showsFirstRun, type FirstRunChoice } from "./firstRunStart";
import { panelViews } from "./cards";
import { startOutcome, type AcceptedStart } from "./start";
import { useStart } from "./useStart";
import { watchTips } from "./tips";
import { QuickTourDialog } from "./quick-tour-dialog";
import { VOICE_HINT_KEY } from "./voice";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { TrainPanel } from "./train-panel";
import { SMALL_SCREEN } from "./small-screen";
import { useVisualViewport } from "./use-visual-viewport";
import { MessagesView } from "./messages-view";
import { Composer } from "./composer";
import { useTerminalLink } from "./terminal-link";
import { liveCfoSession } from "./workflow";
import { messagesQuery } from "./session-messages";
import { useChatStyle } from "./chat-style";
import { goblinName } from "./task-words";

// The terminals load xterm, so the deck arrives the first time one is shown.
const TerminalDeck = boardPage(() => import("./TerminalDeck").then((module) => module.TerminalDeck), "terminals");
// The build this tab last reloaded onto the supervisor's because a page could
// not arrive, kept for the tab's life. A browser that cannot keep it never
// reloads for a missing page, since it could not tell a second reload from
// the first.
const MISSING_PAGE_RELOAD_KEY = "cfo-missing-page-reload";

const PANE_WIDTH_KEY = "cfo-pane-width";
// Recorded once this browser has shown the board, so its first open, and
// only that one, arranges the panel for him.
const FIRST_OPEN_KEY = "cfo-first-open";
// "due" from this browser's first open until its quick tour shows, then
// "seen", so the tour shows once whatever becomes of the layout's own record.
const TOUR_KEY = "cfo-tour";

// The board build this page was loaded with, which the supervisor names in
// the page; a tab left open across an install keeps the older one.
const LOADED_BUILD = document.querySelector<HTMLMetaElement>('meta[name="cfo-build"]')?.content || "";
// The Overlord is in the middle of an answer while the Command Center is open,
// any card in it or any comment on a diff keeps an answer not yet sent, a
// terminal is listening to his dictation, or any text field holds text he
// typed.
const answering = (unsent: boolean) => unsent || !!document.querySelector("dialog[open], .terminal-listening")
  || [...document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("textarea, input:not([type]), input[type=text]")].some((field) => field.value.trim() !== "");

function stored(key: string): string | null {
  try { return localStorage.getItem(key); } catch { return null; }
}
// store says whether this browser kept the value; what it could not keep
// still applies to this view.
function store(key: string, value: string): boolean {
  try { localStorage.setItem(key, value); return true; } catch { return false; }
}

// The board's layout, kept in this browser: kanban unless stacked was chosen.
const BOARD_LAYOUT_KEY = "cfo-board-layout";
// The board's area at or under this width is one column whatever the layout:
// the 960 px container rule on .task-board in styles.css, which
// tests/kanban-board.spec.ts holds to this number.
const KANBAN_NEEDS_OVER = 960;
// A panel at least this wide draws its task list as a column beside its
// messages or terminal, and a narrower one folds it under its header.
const SIDE_NEEDS = 900;

export function App() {
  const [windowFeedback, showWindowFeedback] = useClickFeedback();
  const { snapshot: received, connection } = useRuntimeStream();
  // What the board draws holds closed every item it knows to be closed, so
  // nothing he acted on waits for the next snapshot to leave, and shows his
  // click on a task, Starting, Pausing, Resuming or Stopping, in the frame he
  // clicks.
  const { snapshot: held, sent } = useItemState(received);
  const clicks = useTaskClicks();
  const snapshot = useMemo(() => held && withClicks(held, clicks), [held, clicks]);
  const [view, setView] = useState<"Board" | "Orchestration">("Board");
  const [boardLayout, setBoardLayout] = useState<BoardLayout>(() => stored(BOARD_LAYOUT_KEY) === "stacked" ? "stacked" : "kanban");
  const nextLayout: BoardLayout = boardLayout === "kanban" ? "stacked" : "kanban";
  const [commandFocus, setCommandFocus] = useState<CommandFocus | null>(null);
  const [selected, setSelected] = useState<Selection | null>(null);
  // The CFO chosen by name, which the Board shows in the panel too.
  const [cfoOpen, setCfoOpen] = useState(false);
  // The column beside the panel's stage that its task list is drawn into,
  // and whether the panel is wide enough for it.
  const [side, setSide] = useState<HTMLElement | null>(null);
  const [isPaneWide, setPaneWide] = useState(false);
  const [selectionEpoch, setSelectionEpoch] = useState(0);
  const [paneOpen, setPaneOpen] = useState(true);
  const [paneSize, setPaneSize] = useState<number | null>(() => Number(stored(PANE_WIDTH_KEY)) || null);
  const [resizing, setResizing] = useState(false);
  const [maximizedChoice, setMaximizedChoice] = useState(() => ({ task: stored(MAXIMIZED_KEYS.task), terminal: stored(MAXIMIZED_KEYS.terminal) }));
  const [terminalOpened, setTerminalOpened] = useState(false);
  // The session whose Messages view was last shown, which stays mounted.
  const [messagesOpened, setMessagesOpened] = useState("");
  const [switchFocus, setSwitchFocus] = useState(0);
  // A count each hand of the keyboard to a Messages view's box adds to.
  const [boxFocus, setBoxFocus] = useState(0);
  // The session whose terminal stepped into its messages' place, and the
  // session whose box last typed into its terminal, until its harness opens a
  // screen of its own or starts a turn.
  const [steppedIn, setSteppedIn] = useState("");
  const [sentFrom, setSentFrom] = useState<{ terminal: string; submission: string } | null>(null);
  const [compact, setCompact] = useState(() => matchMedia(SMALL_SCREEN).matches);
  const [firstRunChoice, setFirstRunChoice] = useState<FirstRunChoice>("");
  // startingShown is whether this page already opened the terminal of a CFO
  // that is starting, so closing it is not undone.
  const [startingShown, setStartingShown] = useState(false);
  // opensFirst is whether this is the first time this browser shows the
  // board.
  const [opensFirst, setOpensFirst] = useState(() => firstOpen(stored(FIRST_OPEN_KEY), [stored(PANE_WIDTH_KEY), stored(MAXIMIZED_KEYS.task), stored(MAXIMIZED_KEYS.terminal)]));
  useEffect(() => { if (!opensFirst) store(FIRST_OPEN_KEY, "shown"); }, [opensFirst]);
  // tourDue is whether this browser still owes its quick tour: from its
  // first open until the tour starts, through any reload, and never again
  // once it has had one. A browser that keeps nothing, so that every open
  // looks like its first, is owed none, since it could not keep the tour to
  // once. The tour's first step says how to speak to the terminal, so a
  // tour owed spends the terminal's own voice hint.
  const [tourDue, setTourDue] = useState(() => stored(TOUR_KEY) === "due" || stored(TOUR_KEY) === null && opensFirst && store(TOUR_KEY, "due"));
  useEffect(() => {
    store(TOUR_KEY, tourDue ? "due" : "seen");
    if (tourDue) store(VOICE_HINT_KEY, "dismissed");
  }, [tourDue]);
  const [touring, setTouring] = useState(false);
  // awaitingStart is the Start the Overlord made, until its task is up or a
  // snapshot of it shows it failed.
  const [awaitingStart, setAwaitingStart] = useState<AcceptedStart | null>(null);
  const cardStart = useStart(snapshot, setAwaitingStart);
  const returnFocus = useRef<HTMLElement | null>(null);
  const pane = useRef<HTMLElement>(null);
  const workspace = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLElement>(null);
  // Whether the board's area is too narrow for its columns side by side; a
  // board hidden behind a maximized panel has no width to judge.
  const [boardNarrow, setBoardNarrow] = useState(false);
  useEffect(watchTips, []);
  useEffect(() => {
    const query = matchMedia(SMALL_SCREEN);
    const changed = () => setCompact(query.matches);
    query.addEventListener("change", changed);
    return () => query.removeEventListener("change", changed);
  }, []);
  const node = snapshot?.sessions.find((node) => node.id === selected?.session);
  const selectedTaskID = selected?.task || node?.task_id;
  const task = snapshot?.tasks.find((task) => task.id === selectedTaskID)
    || snapshot?.tasks.find((task) => task.id === "finished:" + selectedTaskID);
  const selectedSession = task?.archived && (!node || node.role === "goblin") ? undefined
    : node || (task && snapshot?.sessions.find((session) => ownsTaskSession(session, task)));
  // A baby goblin opened from its goblin's family tree.
  const child = selected?.child ? task?.tree?.children.find((each) => each.id === selected.child) : undefined;
  // A merge train opened from its card.
  const train = selected?.train ? snapshot?.merge_trains?.find((each) => each.id === selected.train) : undefined;
  const reviews = useReview(task, snapshot);
  const [now,setNow]=useState(Date.now);
  useEffect(()=>{const timer=setInterval(()=>setNow(Date.now()),1000);return()=>clearInterval(timer);},[]);
  const served = snapshot?.build || "";
  const connected = connection === "Live";
  const [hasUnsentAnswers, setHasUnsentAnswers] = useState(false);
  const hasUnsent = hasUnsentAnswers || Object.values(reviews.drafts).some((draft) => unsentComment(draft, reviews.outcome(draft)));
  const isPageMissing = usePageMissing();
  const updated = isPageMissing || updateAction({ loaded: LOADED_BUILD, served, hidden: false, answering: true, connected }) !== "none";
  const hasRecentUpdate = updateSucceededRecently(snapshot, now);
  useEffect(() => {
    const check = () => {
      if (updateAction({ loaded: LOADED_BUILD, served, hidden: document.hidden, answering: answering(hasUnsent), connected, unsent: hasUnsent, updated: hasRecentUpdate }) === "reload") location.reload();
    };
    check();
    document.addEventListener("visibilitychange", check);
    return () => document.removeEventListener("visibilitychange", check);
  }, [served, connected, hasRecentUpdate, hasUnsent]);
  useEffect(() => {
    if (!isPageMissing) return;
    let hasReloaded = true;
    try { hasReloaded = sessionStorage.getItem(MISSING_PAGE_RELOAD_KEY) === LOADED_BUILD; } catch { /* no record, so no reload */ }
    if (missingPageAction({ hasReloaded, answering: answering(hasUnsent), connected }) !== "reload") return;
    try { sessionStorage.setItem(MISSING_PAGE_RELOAD_KEY, LOADED_BUILD); } catch { return; }
    location.reload();
  }, [isPageMissing, connected, hasUnsent]);
  // The desktop window moves onto the program an update installed once
  // this page is idle.
  useWindowMove(snapshot?.instance || "", connected, useCallback(() => answering(hasUnsent), [hasUnsent]));
  useAppBadgeRenewal(snapshot?.instance || "");
  const effects = useActivity(snapshot, connected);
  const presentations=snapshot&&connected?livePresentations(snapshot,now):[];
  // A selection opens its panel, its messages or its terminal beside its task
  // list.
  const select = (next: Selection, source: HTMLElement) => {
    returnFocus.current = source;
    // An empty selection is the supervisor root drawn for the CFO.
    const session = snapshot?.sessions.find((session) => session.id === next.session);
    const cfo = !next.session && !next.task && !next.train || session?.role === "cfo";
    setSelected(cfo ? null : { ...next, task: next.task || session?.task_id });
    setCfoOpen(cfo);
    setSelectionEpoch((epoch) => epoch + 1);
    setPaneOpen(true);
    requestAnimationFrame(() => pane.current?.focus({ preventScroll: true }));
  };
  // keyboardTo hands the keyboard to the session of key: the box under its
  // messages, or its terminal, where the board does not read it as messages
  // or its terminal stepped in.
  const keyboardTo = (key: string) => {
    const hasMessages = !!snapshot && !!(key === CFO_KEY ? messagesQuery(snapshot) : messagesQuery(snapshot, snapshot.tasks.find((each) => each.id === key)));
    if (hasMessages && steppedIn !== key) setBoxFocus((prior) => prior + 1);
    else setSwitchFocus((prior) => prior + 1);
  };
  // A switch, by its keys or from the board, shows that session at once and
  // hands it the keyboard, unless the caller keeps the keyboard where it is.
  const switchTo = (key: string, takesKeyboard = true) => {
    if (key === CFO_KEY) { setSelected(null); setCfoOpen(true); } else { setSelected({ task: key }); setCfoOpen(false); }
    setPaneOpen(true);
    if (takesKeyboard) keyboardTo(key);
  };
  // The CFO's terminal, opened from its bar on the board or its card on the
  // canvas, hands focus back to that button when it closes.
  const openCfo = (source: HTMLElement) => { returnFocus.current = source; switchTo(CFO_KEY); };
  if (firstRunChoice === "started" && snapshot?.cfo_runs) setFirstRunChoice("");
  // A CFO starting in its terminal has not registered yet and may be asking
  // something there, such as Claude Code's sign-in, so the board opens that
  // terminal by itself, once.
  if (snapshot?.cfo_starting && !startingShown) { setStartingShown(true); setView("Board"); switchTo(CFO_KEY); }
  // A CFO that was closed starts again in its terminal when it is reopened,
  // and that terminal opens by itself once more.
  if (snapshot?.cfo_closed && startingShown) setStartingShown(false);
  // The tour starts on the Board with the CFO's panel beside it, leaving the
  // keyboard to the tour; its end hands the keyboard to that panel's view,
  // the box under its messages or its terminal, except in a window too
  // narrow for the two side by side.
  const startTour = () => { setView("Board"); switchTo(CFO_KEY, false); setTouring(true); };
  const endTour = () => { setTouring(false); if (!compact) keyboardTo(CFO_KEY); };
  // A task the Overlord started opens on its terminal once its session is up;
  // a start that failed shows why on its card instead.
  const outcome = awaitingStart && snapshot ? startOutcome(awaitingStart, snapshot) : "wait";
  if (awaitingStart && outcome !== "wait") { setAwaitingStart(null); if (outcome === "open") { setView("Board"); switchTo(awaitingStart.id); } }
  // A home that already chose its CFO stays on the board while it comes back.
  const firstRun = !!snapshot && showsFirstRun({ cfoRuns: snapshot.cfo_runs, cfoClosed: snapshot.cfo_closed, cfoConfigured: snapshot.cfo_configured, choice: firstRunChoice });
  // The first time this browser shows the board of a home whose CFO runs, it
  // opens as the Overlord keeps his own: the Board with the CFO's terminal
  // beside it and the keyboard in that terminal. On a small screen an open
  // panel takes the whole screen, so there the board shows alone. A tour owed
  // takes the keyboard first.
  if (opensFirst && snapshot && !firstRun) {
    setOpensFirst(false);
    if (snapshot.cfo_runs && !compact) { setView("Board"); switchTo(CFO_KEY, !tourDue); }
  }
  // The tour begins at the CFO's terminal, so a tour owed waits for a CFO
  // that runs, such as one the first-run page started.
  if (tourDue && snapshot?.cfo_runs && !firstRun) { setTourDue(false); startTour(); }
  useEffect(() => {
    if (!canvas.current) return;
    const observer = new ResizeObserver(([entry]) => setBoardNarrow(entry.contentRect.width > 0 && entry.contentRect.width <= KANBAN_NEEDS_OVER));
    observer.observe(canvas.current);
    return () => observer.disconnect();
  }, [firstRun]);
  useEffect(() => {
    if (!pane.current) return;
    const observer = new ResizeObserver(([entry]) => setPaneWide(entry.contentRect.width >= SIDE_NEEDS));
    observer.observe(pane.current);
    return () => observer.disconnect();
  }, [firstRun]);
  const close = () => {
    setPaneOpen(false);
    requestAnimationFrame(() => returnFocus.current?.isConnected && returnFocus.current.focus());
  };
  const cfoShown = !selected && (view === "Orchestration" || cfoOpen);
  const showsPanel = !!snapshot && paneOpen && (view === "Orchestration" || !!task || !!train || cfoOpen);
  // The session whose messages the panel shows: the CFO's, or a task's own
  // session, never a child's (messagesQuery).
  const messagesSource = !snapshot || !showsPanel || train || child ? ""
    : cfoShown ? messagesQuery(snapshot)
      : selected && task && (!selectedSession || ownsTaskSession(selectedSession, task)) ? messagesQuery(snapshot, task) : "";
  const views = panelViews(selected ? task : undefined, selected ? selectedSession : undefined);
  // A queued task has no terminal yet, so its panel is its task list alone,
  // and a baby goblin's panel is its terminal alone.
  const shownView: PanelView = child || views.includes("terminal") ? "terminal" : "task";
  // The session's own terminal, by the terminal deck's key, whether its
  // harness works, which the box's working line shows, and that terminal's
  // link, which says what it was last told of the harness's screen.
  const terminalKey = cfoShown ? CFO_KEY : task?.id || "";
  const isWorking = ["busy", "working", "active"].includes((cfoShown ? snapshot && liveCfoSession(snapshot)?.runtime?.state : task?.runtime?.state) || "");
  const live = useTerminalLink(terminalKey);
  // A session the board reads as messages shows them, with its terminal laid
  // out behind them in the same box for its box to type through. The terminal steps into their place, with the keyboard,
  // when what the box sent opens a screen of the harness's own before a turn
  // starts, a picker, a panel or a question, and steps out by itself once the
  // harness's input line is back, the keyboard going back to the box. The
  // messages stay mounted from their first showing while the panel shows the
  // session, so a switch back reads nothing again and finds the place he read.
  const isSteppedIn = !!messagesSource && !!terminalKey && steppedIn === terminalKey;
  // Classic draws the harness's own terminal in the conversation's place and
  // Modern draws the conversation, from one saved choice. The conversation
  // stays mounted under the terminal either way, so a change of style keeps
  // the session, its history, an unsent draft and the place he read.
  // Classic keeps the harness's own terminal in the conversation's place
  // until he steps out of it for this session, and Modern shows the
  // conversation until a screen or a tap steps the terminal in. The saved
  // choice is the only thing that decides it, so a change of style carries
  // the session, its history, an unsent draft and the place he read.
  const [chatStyle] = useChatStyle();
  const [classicOut, setClassicOut] = useState("");
  const classicInPlace = chatStyle === "classic" && !!terminalKey && classicOut !== terminalKey;
  const terminalInPlace = isSteppedIn || classicInPlace;
  if (sentFrom && (sentFrom.terminal !== terminalKey || live?.isReconnecting || live?.phase === "closed")) setSentFrom(null)
  else if (sentFrom && live?.screen?.submission === sentFrom.submission) {
    if (live.screen.hasStarted || live.screen.isWorking) setSentFrom(null)
    else if (live.screen.isOpen) { setSentFrom(null); setSteppedIn(terminalKey); setSwitchFocus((prior) => prior + 1); }
  }
  if (isSteppedIn && live?.screen && !live.screen.isOpen) { setSteppedIn(""); setBoxFocus((prior) => prior + 1); }
  const stepIn = () => { setClassicOut(""); setSteppedIn(terminalKey); setSwitchFocus((prior) => prior + 1); };
  const stepOut = () => { setClassicOut(terminalKey); setSteppedIn(""); setBoxFocus((prior) => prior + 1); };
  const terminalShown = showsPanel && !train && shownView === "terminal" && (!messagesSource || terminalInPlace);
  const messagesShown = showsPanel && !train && shownView === "terminal" && !!messagesSource && !terminalInPlace;
  if ((terminalShown || messagesShown) && !terminalOpened) setTerminalOpened(true);
  if (messagesShown && messagesSource !== messagesOpened) setMessagesOpened(messagesSource);
  useSwitchKeys(snapshot ? switchOrder(snapshot.tasks) : [], cfoShown ? CFO_KEY : task?.id || "", switchTo);
  const maximizeView = maximizedView(view, shownView);
  const maximized = maximizedFor(maximizedChoice[maximizeView]);
  // Only a panel that shows something is maximized: with nothing chosen it
  // shows the review placeholder, which takes no view's choice, so a
  // terminal maximized before a reload never hides the board behind it.
  const panelWide = showsPanel && maximized && !compact;
  // On a small screen a panel opened on anything, a task, a train or the CFO,
  // is the whole view below the top bar, and its corner's Back returns to the
  // board or the graph it was opened from (the Overlord, 2026-10-20: "It
  // should become the full screen below the header banner"). The tour, which
  // lights the board and the panel in turn, keeps them stacked.
  const fillsPhone = compact && showsPanel && !touring && (!!selected || cfoOpen);
  useVisualViewport(fillsPhone);
  // Back, on the panel of anything but the CFO, returns the panel to the
  // CFO's, still maximized if it was, and hands
  // the keyboard back to where the panel was opened from, or to the panel
  // when that is out of sight behind it.
  const backShown = showsPanel && !!selected;
  const back = () => {
    if (maximized) setMaximizedChoice((prior) => ({ ...prior, [maximizedView(view, "terminal")]: "true" }));
    setSelected(null);
    setCfoOpen(true);
    requestAnimationFrame(() => {
      returnFocus.current?.focus();
      if (document.activeElement !== returnFocus.current) pane.current?.focus({ preventScroll: true });
    });
  };
  const divided = paneOpen && !panelWide && !compact;
  const layout: CSSProperties | undefined = panelWide ? { gridTemplateColumns: "minmax(0, 1fr)" } : paneOpen && paneSize && !compact ? { gridTemplateColumns: `minmax(0, 1fr) 10px ${paneTrack(paneSize)}` } : undefined;
  // Open in terminal shows the terminal in a Windows Terminal window of its
  // own, beside the board; a refusal says so under the button for a moment.
  // A window on the PC is nothing to a phone, so a small screen has no such
  // button.
  const shownWindow = snapshot && (terminalShown || messagesShown) && !child ? windowTarget(snapshot, cfoShown, selected ? task : undefined) : null;
  const shownKey = JSON.stringify(shownWindow);
  const openWindow = async () => {
    if (!snapshot || !shownWindow) return;
    showWindowFeedback("");
    try {
      await request("/api/terminal/open", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: shownKey });
    } catch (error: unknown) { showWindowFeedback(message(error)); }
  };
  // The panel's top row (see PanelRow): its controls in the order they are
  // drawn, its corner button, and what Open in terminal met.
  const toggleMaximized = () => {
    const choice = String(!maximized);
    setMaximizedChoice((prior) => ({ ...prior, [maximizeView]: choice }));
    store(MAXIMIZED_KEYS[maximizeView], choice);
  };
  const controls: PanelControl[] = [];
  if (shownWindow && !compact) controls.push({ id: "window", name: "Open in terminal", label: "Open in Windows Terminal", icon: "external", importance: PANEL_IMPORTANCE.window, onPress: () => void openWindow() });
  if (!compact) controls.push({ id: "maximize", name: maximized ? "Restore" : "Maximize", label: maximized ? "Restore the panel" : "Maximize the panel", icon: maximized ? "restore" : "maximize", importance: PANEL_IMPORTANCE.maximize, onPress: toggleMaximized });
  const row = {
    controls,
    corner: fillsPhone
      ? <button className="labelled-button" aria-label={"Back to " + view} onClick={close}><Icon name="back" /><span>Back</span></button>
      : backShown
      ? <button className="labelled-button" aria-label="Back to the CFO" onClick={back}><Icon name="back" /><span>Back</span></button>
      : <button className="icon-button" aria-label="Close panel" data-tip="Close" onClick={close}><Icon name="close" /></button>,
    notice: windowFeedback && <span className="window-feedback"><ClickFeedback text={windowFeedback} /></span>,
  };
  return <AfkBoard snapshot={snapshot} now={now} onOpenCommand={(key) => setCommandFocus({ key, at: Date.now() })}><div className={"app-shell" + (fillsPhone ? " fills-phone" : "")} onKeyDown={(event) => {
    if (event.key === "Escape" && paneOpen && !event.defaultPrevented) { event.preventDefault(); if (backShown && !fillsPhone) back(); else close(); }
  }}>
    <header className="topbar">
      <a className="brand" href="/" aria-label="Code Goblins home">
        <img src="/assets/goblin-app.png" width="48" height="48" alt="" /><h1>Code Goblins</h1>
        {snapshot?.example && <span className="example-label">Example workspace</span>}
      </a>
      <div className="view-switch" role="group" aria-label="Workspace view">
        {(["Board", "Orchestration"] as const).map((name) => <button key={name} aria-pressed={!firstRun && view === name} onClick={() => { if (firstRun) setFirstRunChoice("board"); setView(name); }}>{name}</button>)}
      </div>
      <div className="topbar-controls">
        {/* A small screen has no room for the tour's button, nor for the
            layout's, since a board that narrow is always stacked. */}
        {snapshot && !firstRun && !compact && <button className="icon-button" aria-label="Replay the tour" data-tip="Replay the tour" onClick={startTour}><Icon name="question" /></button>}
        {/* A kanban board too narrow for columns side by side is stacked, so
            there the button changes nothing and says why; a board stacked by
            choice keeps its way back, since the panel yields to a kanban. */}
        {!firstRun && view === "Board" && !compact && (boardNarrow && boardLayout === "kanban"
          ? <button className="icon-button" aria-disabled="true" aria-label="Layout: stacked, the board is too narrow for columns side by side" data-tip="Too narrow for columns side by side, so the board is stacked. Close or narrow the panel, or widen the window."><Icon name="stacked" /></button>
          : <button className="icon-button" aria-label={nextLayout === "stacked" ? "Stacked layout" : "Kanban layout"} data-tip={nextLayout === "stacked" ? "Stacked layout" : "Kanban layout"}
            onClick={() => { setBoardLayout(nextLayout); store(BOARD_LAYOUT_KEY, nextLayout); }}><Icon name={boardLayout} /></button>)}
        {snapshot && <CommandCenter snapshot={snapshot} connected={connected} presentations={presentations} focus={commandFocus} onUnsent={setHasUnsentAnswers} onSent={sent} />}
        <div className="connection" role="status">
          <span className={"live-dot " + (!connected ? "offline" : "")} />{connection}
        </div>
        {!paneOpen && !compact && <button onClick={() => setPaneOpen(true)}>Open {selected ? "details" : "CFO"}</button>}
      </div>
    </header>
    {updated && <div className="update-banner" role="status"><span>The board was updated.</span><button className="primary" onClick={() => location.reload()}>Reload</button></div>}
    {snapshot && <ComebackBanner comeback={snapshot.comeback} />}
    {snapshot && <Alerts snapshot={snapshot} onOpen={(key) => setCommandFocus({ key, at: Date.now() })} />}
    {firstRun ? <main className="first-run-region" aria-label="First run">
      {snapshot && <FirstRun instance={snapshot.instance} devDrive={snapshot.dev_drive} onStarting={() => setFirstRunChoice("setup")} onStarted={() => { setFirstRunChoice("started"); setView("Board"); switchTo(CFO_KEY); }} onBoard={() => setFirstRunChoice("board")} />}
    </main> : <div ref={workspace} className={"workspace" + (paneOpen ? " with-pane" : "") + (view === "Board" && boardLayout === "kanban" ? " kanban" : "") + (resizing && divided ? " resizing" : "")} style={layout}>
      <main ref={canvas} className="canvas-region" aria-label={view} hidden={panelWide || fillsPhone}>
        {!snapshot || !cardStart ? <div className="empty-state" role="status"><h2>Connecting to the supervisor</h2><p>Loading tasks and native sessions.</p></div>
          : view === "Board" ? <Board presentations={presentations} snapshot={snapshot} layout={boardLayout} selected={task?.id} selectedTrain={train?.id} now={now} onSelect={(task, source) => select({ task: task.id }, source)} onSelectTrain={(train, source) => select({ train: train.id }, source)} onOpenCfo={openCfo} onOpenCommand={() => setCommandFocus({ key: "", at: Date.now() })} onStartCfo={() => setFirstRunChoice("")} />
            : compact ? <Lineage onOpenCfo={openCfo} presentations={presentations} effects={effects} snapshot={snapshot} now={now} project="" selected={!child && selectedSession ? { session: selectedSession.id } : selected} onSelect={select} />
              : <Orchestration presentations={presentations} effects={effects} snapshot={snapshot} now={now} connected={connected}
                selected={child ? "child:" + child.id : selectedSession ? "session:" + selectedSession.id : selected?.task ? "task:" + selected.task : ""}
                onSelect={(node, source) => select(node.session ? { session: node.session.id } : node.task ? { task: node.task.id } : {}, source)}
                onChild={(task, baby, source) => select(childSelection(task, baby), source)} onOpenCfo={openCfo} />}
      </main>
      {divided && <PaneDivider workspace={workspace} pane={pane} width={paneSize} onWidth={setPaneSize} onResizing={setResizing} onDone={(width) => store(PANE_WIDTH_KEY, String(width))} />}
      <aside ref={pane} className="context-pane" hidden={!paneOpen} tabIndex={-1} aria-label={view === "Board" ? "Task review" : "Goblin panel"}>
        {snapshot && cardStart && paneOpen && (!showsPanel
          ? <><PanelRow {...row} />
            <section className="review-placeholder"><Avatar persona="reviewer" /><h2>Review the work</h2><p>Select a task to see what it is doing and what changed.</p></section></>
          : train ? <TrainPanel key={selectionEpoch + ":train:" + train.id} train={train} tasks={snapshot.tasks} row={row} />
          : <GoblinPanel key={selectionEpoch + ":" + (selectedSession?.id || task?.id || "cfo") + ":" + (task?.generation || "") + ":" + (child?.id || "")}
            task={selected ? task : undefined} node={selected ? selectedSession : undefined} child={child} snapshot={snapshot} connected={connected} reviews={reviews}
            taskPlace={shownView === "task" ? "alone" : compact || !isPaneWide ? "fold" : "side"} side={side} now={now} presentations={presentations} cardStart={cardStart} row={row} onAnswer={(key) => setCommandFocus({ key, at: Date.now() })}
            onOpenTask={(next) => select({ task: next.id }, pane.current || document.body)} />)}
        {/* The stage, the Messages view and the terminal deck in one box,
            beside the column the panel's task list is drawn into. */}
        <div className="panel-body" hidden={!terminalShown && !messagesShown}>
        <PanelStage hidden={!terminalShown && !messagesShown} isSteppedIn={isSteppedIn} onBack={stepOut}>
          {snapshot && messagesSource && messagesSource === messagesOpened && <MessagesView key={messagesSource} query={messagesSource} instance={snapshot.instance}
            name={cfoShown || !task ? "CFO" : goblinName(task)} task={cfoShown ? undefined : task} shown={messagesShown} visible={connected}
            onChild={(baby, source) => { if (task) select(childSelection(task, baby), source); }}
            box={<Composer snapshot={snapshot} task={cfoShown ? undefined : task} name={cfoShown || !task ? "CFO" : goblinName(task)} terminal={terminalKey} isWorking={isWorking}
              focus={messagesShown ? boxFocus : 0} onAnswer={stepIn} onSent={(submission) => setSentFrom({ terminal: terminalKey, submission })} />} />}
          {snapshot && terminalOpened && <Suspense fallback={terminalShown ? <div className="terminal-deck"><div className="deck-stage"><div className="terminal-cover" role="status"><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the terminal</p></div></div></div> : null}>
            <TerminalDeck snapshot={snapshot} task={selected ? task : undefined} node={selected ? selectedSession : undefined} child={child} cfo={cfoShown} shown={terminalShown || messagesShown} behind={messagesShown} connected={connected} focus={switchFocus}
              onOwner={task && snapshot.sessions.some((session) => ownsTaskSession(session, task)) ? () => { setSelected({ task: task.id }); setSelectionEpoch((epoch) => epoch + 1); } : undefined} />
          </Suspense>}
        </PanelStage>
        {!compact && isPaneWide && <div ref={setSide} className="panel-side" hidden={!!child} />}
        </div>
      </aside>
    </div>}
    {touring && <QuickTourDialog onEnd={endTour} />}
  </div></AfkBoard>;
}
