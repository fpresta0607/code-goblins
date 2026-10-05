import { lazy, Suspense, useCallback, useEffect, useRef, useState, type CSSProperties } from "react";
import { useRuntimeStream } from "./stream";
import { useItemState } from "./use-item-state";
import { Lineage, type Selection } from "./Lineage";
import { Board, type BoardLayout } from "./Board";
import { Orchestration } from "./Orchestration";
import { CommandCenter, type CommandFocus } from "./CommandCenter";
import { useActivity } from "./useActivity";
import { livePresentations } from "./activity";
import { useReview } from "./review";
import { ownsTaskSession } from "./lineageTree";
import { Icon } from "./Icon";
import { Avatar } from "./Avatar";
import { GoblinPanel, type PanelView } from "./GoblinPanel";
import { PaneDivider } from "./PaneDivider";
import { PANEL_IMPORTANCE, PanelRow, type PanelControl } from "./panel-row";
import { CFO_KEY, MAXIMIZED_KEYS, firstOpen, maximizedFor, maximizedView, paneTrack, switchOrder } from "./terminalOrder";
import { useSwitchKeys } from "./useSwitchKeys";
import { unsentComment, updateAction } from "./boardUpdate";
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

// The terminals load xterm, so the deck arrives the first time one is shown.
const TerminalDeck = lazy(() => import("./TerminalDeck").then((module) => ({ default: module.TerminalDeck })));

const PANE_WIDTH_KEY = "cfo-pane-width";
// Recorded once this browser has shown the board, so its first open, and
// only that one, arranges the panel for him.
const FIRST_OPEN_KEY = "cfo-first-open";

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
function store(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* the layout still applies to this view */ }
}

// The board's layout, kept in this browser: kanban unless stacked was chosen.
const BOARD_LAYOUT_KEY = "cfo-board-layout";
// The board's area at or under this width is one column whatever the layout:
// the 960 px container rule on .task-board in styles.css, which
// tests/kanban-board.spec.ts holds to this number.
const KANBAN_NEEDS_OVER = 960;

export function App() {
  const { snapshot: received, connection, error } = useRuntimeStream();
  // What the board draws holds closed every item it knows to be closed, so
  // nothing he acted on waits for the next snapshot to leave.
  const { snapshot, sent } = useItemState(received);
  const [view, setView] = useState<"Board" | "Orchestration">("Board");
  const [boardLayout, setBoardLayout] = useState<BoardLayout>(() => stored(BOARD_LAYOUT_KEY) === "stacked" ? "stacked" : "kanban");
  const nextLayout: BoardLayout = boardLayout === "kanban" ? "stacked" : "kanban";
  // Board opens a goblin on its task view, Orchestration on its terminal.
  const [panelView, setPanelView] = useState<PanelView>("task");
  const [commandFocus, setCommandFocus] = useState<CommandFocus | null>(null);
  const [selected, setSelected] = useState<Selection | null>(null);
  // The CFO chosen by name, which the Board shows in the panel too.
  const [cfoOpen, setCfoOpen] = useState(false);
  // The view the CFO's own panel last showed, which Back returns to.
  const [cfoPanelView, setCfoPanelView] = useState<PanelView | null>(null);
  const [selectionEpoch, setSelectionEpoch] = useState(0);
  const [paneOpen, setPaneOpen] = useState(true);
  const [paneSize, setPaneSize] = useState<number | null>(() => Number(stored(PANE_WIDTH_KEY)) || null);
  const [resizing, setResizing] = useState(false);
  const [maximizedChoice, setMaximizedChoice] = useState(() => ({ task: stored(MAXIMIZED_KEYS.task), terminal: stored(MAXIMIZED_KEYS.terminal) }));
  const [terminalOpened, setTerminalOpened] = useState(false);
  const [switchFocus, setSwitchFocus] = useState(0);
  // windowError is a refused Open in terminal and the terminal it was for.
  const [windowError, setWindowError] = useState({ shown: "", text: "" });
  const [compact, setCompact] = useState(() => matchMedia("(max-width: 40rem)").matches);
  const [firstRunChoice, setFirstRunChoice] = useState<FirstRunChoice>("");
  // startingShown is whether this page already opened the terminal of a CFO
  // that is starting, so closing it is not undone.
  const [startingShown, setStartingShown] = useState(false);
  // opensFirst is whether this is the first time this browser shows the
  // board. A first-open tour would start where it is spent, below.
  const [opensFirst, setOpensFirst] = useState(() => firstOpen(stored(FIRST_OPEN_KEY), [stored(PANE_WIDTH_KEY), stored(MAXIMIZED_KEYS.task), stored(MAXIMIZED_KEYS.terminal)]));
  useEffect(() => { if (!opensFirst) store(FIRST_OPEN_KEY, "shown"); }, [opensFirst]);
  // awaitingStart is the Start the Overlord made, until its task is up or a
  // snapshot of it shows it failed.
  const [awaitingStart, setAwaitingStart] = useState<AcceptedStart | null>(null);
  const cardStart = useStart(snapshot, awaitingStart, setAwaitingStart);
  const returnFocus = useRef<HTMLElement | null>(null);
  const pane = useRef<HTMLElement>(null);
  const workspace = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLElement>(null);
  // Whether the board's area is too narrow for its columns side by side; a
  // board hidden behind a maximized panel has no width to judge.
  const [boardNarrow, setBoardNarrow] = useState(false);
  useEffect(watchTips, []);
  useEffect(() => {
    const query = matchMedia("(max-width: 40rem)");
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
  const reviews = useReview(task, snapshot);
  // Once the supervisor serves a newer board, a hidden tab reloads itself at
  // once and a visible one says so and offers a reload, never mid-answer.
  const served = snapshot?.build || "";
  const connected = connection === "Live";
  const unsent = useRef(false);
  const onUnsent = useCallback((next: boolean) => { unsent.current = next; }, []);
  const commenting = Object.values(reviews.drafts).some((draft) => unsentComment(draft, reviews.outcome(draft)));
  const commentUnsent = useRef(false);
  useEffect(() => { commentUnsent.current = commenting; }, [commenting]);
  const updated = updateAction({ loaded: LOADED_BUILD, served, hidden: false, answering: true, connected }) !== "none";
  useEffect(() => {
    const check = () => { if (updateAction({ loaded: LOADED_BUILD, served, hidden: document.hidden, answering: answering(unsent.current || commentUnsent.current), connected }) === "reload") location.reload(); };
    check();
    document.addEventListener("visibilitychange", check);
    return () => document.removeEventListener("visibilitychange", check);
  }, [served, connected]);
  const effects = useActivity(snapshot, connected);
  const [now,setNow]=useState(Date.now);
  useEffect(()=>{const timer=setInterval(()=>setNow(Date.now()),1000);return()=>clearInterval(timer);},[]);
  const presentations=snapshot&&connected?livePresentations(snapshot,now):[];
  // Board opens a goblin on its Task view and Orchestration on its Terminal
  // view, unless the caller asks for a view (the card's terminal button).
  const select = (next: Selection, source: HTMLElement, panel: PanelView = view === "Board" ? "task" : "terminal") => {
    returnFocus.current = source;
    // An empty selection is the supervisor root drawn for the CFO.
    const session = snapshot?.sessions.find((session) => session.id === next.session);
    const cfo = !next.session && !next.task || session?.role === "cfo";
    setSelected(cfo ? null : { ...next, task: next.task || session?.task_id });
    setCfoOpen(cfo);
    setPanelView(panel);
    setSelectionEpoch((epoch) => epoch + 1);
    setPaneOpen(true);
    requestAnimationFrame(() => {
      pane.current?.focus({ preventScroll: true });
      if (compact) pane.current?.scrollIntoView({ behavior: "instant", block: "start" });
    });
  };
  // A switch, by its keys or from the board, shows that terminal at once and
  // hands it the keyboard, unless the caller keeps the keyboard where it is.
  const switchTo = (key: string, takesKeyboard = true) => {
    if (key === CFO_KEY) { setSelected(null); setCfoOpen(true); } else { setSelected({ task: key }); setCfoOpen(false); }
    setPanelView("terminal");
    setPaneOpen(true);
    if (takesKeyboard) setSwitchFocus((prior) => prior + 1);
  };
  if (firstRunChoice === "started" && snapshot?.cfo_runs) setFirstRunChoice("");
  // A CFO starting in its terminal has not registered yet and may be asking
  // something there, such as Claude Code's sign-in, so the board opens that
  // terminal by itself, once.
  if (snapshot?.cfo_starting && !startingShown) { setStartingShown(true); setView("Board"); switchTo(CFO_KEY); }
  // A CFO that was closed starts again in its terminal when it is reopened,
  // and that terminal opens by itself once more.
  if (snapshot?.cfo_closed && startingShown) setStartingShown(false);
  // A task the Overlord started opens on its terminal once its session is up;
  // a start that failed shows why on its card instead.
  const outcome = awaitingStart && snapshot ? startOutcome(awaitingStart, snapshot) : "wait";
  if (awaitingStart && outcome !== "wait") { setAwaitingStart(null); if (outcome === "open") { setView("Board"); switchTo(awaitingStart.id); } }
  // The board's root is the first-run page whenever no CFO runs.
  const firstRun = !!snapshot && showsFirstRun({ cfoRuns: snapshot.cfo_runs, cfoClosed: snapshot.cfo_closed, choice: firstRunChoice });
  // The first time this browser shows the board of a home whose CFO runs, it
  // opens as the Overlord keeps his own: the Board with the CFO's terminal
  // beside it and the keyboard in that terminal. A window too narrow for two
  // columns shows the board with the panel under it, so there the keyboard
  // stays where it is and the board stays in view.
  if (opensFirst && snapshot && !firstRun) {
    setOpensFirst(false);
    if (snapshot.cfo_runs) { setView("Board"); switchTo(CFO_KEY, !compact); }
  }
  useEffect(() => {
    if (!canvas.current) return;
    const observer = new ResizeObserver(([entry]) => setBoardNarrow(entry.contentRect.width > 0 && entry.contentRect.width <= KANBAN_NEEDS_OVER));
    observer.observe(canvas.current);
    return () => observer.disconnect();
  }, [firstRun]);
  const close = () => {
    setPaneOpen(false);
    requestAnimationFrame(() => returnFocus.current?.isConnected && returnFocus.current.focus());
  };
  const cfoShown = !selected && (view === "Orchestration" || cfoOpen);
  const showsPanel = !!snapshot && paneOpen && (view === "Orchestration" || !!task || cfoOpen);
  // A queued task has no terminal yet, so its panel shows its Task view.
  const shownView: PanelView = panelViews(selected ? task : undefined, selected ? selectedSession : undefined).includes(panelView) ? panelView : "task";
  const terminalShown = showsPanel && shownView === "terminal";
  if (terminalShown && !terminalOpened) setTerminalOpened(true);
  if (showsPanel && cfoShown && cfoPanelView !== shownView) setCfoPanelView(shownView);
  useSwitchKeys(snapshot ? switchOrder(snapshot.tasks) : [], cfoShown ? CFO_KEY : task?.id || "", switchTo);
  const maximizeView = maximizedView(view, shownView);
  const maximized = maximizedFor(maximizedChoice[maximizeView]);
  const panelWide = paneOpen && maximized && !compact;
  // Back, on the panel of anything but the CFO, returns the panel to the
  // CFO's on the view it last showed, still maximized if it was, and hands
  // the keyboard back to where the panel was opened from, or to the panel
  // when that is out of sight behind it.
  const backShown = showsPanel && !!selected;
  const back = () => {
    const target = cfoPanelView ?? (view === "Board" ? "task" : "terminal");
    if (maximized) setMaximizedChoice((prior) => ({ ...prior, [maximizedView(view, target)]: "true" }));
    setSelected(null);
    setCfoOpen(true);
    setPanelView(target);
    requestAnimationFrame(() => {
      returnFocus.current?.focus();
      if (document.activeElement !== returnFocus.current) pane.current?.focus({ preventScroll: true });
    });
  };
  const divided = paneOpen && !panelWide && !compact;
  const layout: CSSProperties | undefined = panelWide ? { gridTemplateColumns: "minmax(0, 1fr)" } : paneOpen && paneSize && !compact ? { gridTemplateColumns: `minmax(0, 1fr) 10px ${paneTrack(paneSize)}` } : undefined;
  // Open in terminal shows the terminal in a Windows Terminal window of its
  // own, beside the board; a refusal says why under the button.
  const shownWindow = snapshot && terminalShown ? windowTarget(snapshot, cfoShown, selected ? task : undefined) : null;
  const shownKey = JSON.stringify(shownWindow);
  const openWindow = async () => {
    if (!snapshot || !shownWindow) return;
    setWindowError({ shown: "", text: "" });
    try {
      await request("/api/terminal/open", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: shownKey });
    } catch (e: unknown) { setWindowError({ shown: shownKey, text: message(e) }); }
  };
  // The panel's top row (see PanelRow): its controls in the order they are
  // drawn, its corner button, and why Open in terminal was refused.
  const toggleMaximized = () => {
    const choice = String(!maximized);
    setMaximizedChoice((prior) => ({ ...prior, [maximizeView]: choice }));
    store(MAXIMIZED_KEYS[maximizeView], choice);
  };
  const controls: PanelControl[] = [];
  if (shownWindow) controls.push({ id: "window", name: "Open in terminal", label: "Open in Windows Terminal", icon: "external", importance: PANEL_IMPORTANCE.window, onPress: () => void openWindow() });
  if (!compact) controls.push({ id: "maximize", name: maximized ? "Restore" : "Maximize", label: maximized ? "Restore the panel" : "Maximize the panel", icon: maximized ? "restore" : "maximize", importance: PANEL_IMPORTANCE.maximize, onPress: toggleMaximized });
  const row = {
    controls,
    corner: backShown
      ? <button className="labelled-button" aria-label="Back to the CFO" onClick={back}><Icon name="back" /><span>Back</span></button>
      : <button className="icon-button" aria-label="Close panel" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>,
    notice: shownWindow && windowError.shown === shownKey && <p className="window-error" role="alert">{windowError.text}</p>,
  };
  return <AfkBoard snapshot={snapshot} now={now} onCommand={(key) => setCommandFocus({ key, at: Date.now() })}><div className="app-shell" onKeyDown={(event) => {
    if (event.key === "Escape" && paneOpen && !event.defaultPrevented) { event.preventDefault(); if (backShown) back(); else close(); }
  }}>
    <header className="topbar">
      <a className="brand" href="/" aria-label="Code Goblins home">
        <img src="/assets/goblin-app.png" width="48" height="48" alt="" /><h1>Code Goblins</h1>
        {snapshot?.example && <span className="example-label">Example workspace</span>}
      </a>
      <div className="view-switch" role="group" aria-label="Workspace view">
        {(["Board", "Orchestration"] as const).map((name) => <button key={name} aria-pressed={!firstRun && view === name} onClick={() => { if (firstRun) setFirstRunChoice("board"); setView(name); setPanelView(name === "Board" ? "task" : "terminal"); }}>{name}</button>)}
      </div>
      <div className="topbar-controls">
        {/* A kanban board too narrow for columns side by side is stacked, so
            there the button changes nothing and says why; a board stacked by
            choice keeps its way back, since the panel yields to a kanban. */}
        {!firstRun && view === "Board" && (boardNarrow && boardLayout === "kanban"
          ? <button className="icon-button" aria-disabled="true" aria-label="Layout: stacked, the board is too narrow for columns side by side" data-tip="Too narrow for columns side by side, so the board is stacked. Close or narrow the panel, or widen the window." data-tip-align="end"><Icon name="stacked" /></button>
          : <button className="icon-button" aria-label={nextLayout === "stacked" ? "Stacked layout" : "Kanban layout"} data-tip={nextLayout === "stacked" ? "Stacked layout" : "Kanban layout"} data-tip-align="end"
            onClick={() => { setBoardLayout(nextLayout); store(BOARD_LAYOUT_KEY, nextLayout); }}><Icon name={boardLayout} /></button>)}
        {snapshot && <CommandCenter snapshot={snapshot} connected={connected} presentations={presentations} focus={commandFocus} onUnsent={onUnsent} onSent={sent} />}
        <div className="connection" role="status">
          <span className={"live-dot " + (!connected ? "offline" : "")} />{connection}
        </div>
        {!paneOpen && <button onClick={() => setPaneOpen(true)}>Open {selected ? "details" : "CFO"}</button>}
      </div>
    </header>
    {updated && <div className="update-banner" role="status"><span>The board was updated.</span><button className="primary" onClick={() => location.reload()}>Reload</button></div>}
    {snapshot && <Alerts snapshot={snapshot} onOpen={(target) => { if (target.kind === "command") setCommandFocus({ key: target.key, at: Date.now() }); else select({ task: target.id }, document.body, "task"); }} />}
    {firstRun ? <main className="first-run-region" aria-label="First run">
      {snapshot && <FirstRun instance={snapshot.instance} onStarted={() => { setFirstRunChoice("started"); setView("Board"); switchTo(CFO_KEY); }} onBoard={() => setFirstRunChoice("board")} />}
    </main> : <div ref={workspace} className={"workspace" + (paneOpen ? " with-pane" : "") + (view === "Board" && boardLayout === "kanban" ? " kanban" : "") + (resizing && divided ? " resizing" : "")} style={layout}>
      <main ref={canvas} className="canvas-region" aria-label={view} hidden={panelWide}>
        {(error || snapshot?.error) && <div className="connection-banner" role="alert">{error || snapshot?.error}</div>}
        {snapshot?.registration && <div className="connection-banner" role="alert">{snapshot.registration}</div>}
        {snapshot?.cfo_conversation_left && <div className="connection-banner" role="status">{snapshot.cfo_conversation_left}</div>}
        {!snapshot || !cardStart ? <div className="empty-state" role="status"><h2>Connecting to the supervisor</h2><p>Loading tasks and native sessions.</p></div>
          : view === "Board" ? <Board presentations={presentations} snapshot={snapshot} layout={boardLayout} selected={task?.id} now={now} onSelect={(task, source) => select({ task: task.id }, source)} onTerminal={(task, source) => select({ task: task.id }, source, "terminal")} onOpenCfo={(source) => { returnFocus.current = source; switchTo(CFO_KEY); }} onOpenCommand={() => setCommandFocus({ key: "", at: Date.now() })} onStartCfo={() => setFirstRunChoice("")} cardStart={cardStart} />
            : compact ? <Lineage presentations={presentations} effects={effects} snapshot={snapshot} project="" selected={selectedSession ? { session: selectedSession.id } : selected} onSelect={select} />
              : <Orchestration presentations={presentations} effects={effects} snapshot={snapshot} connected={connected} selected={selectedSession ? "session:" + selectedSession.id : selected?.task ? "task:" + selected.task : ""}
                onSelect={(node, source) => select(node.session ? { session: node.session.id } : node.task ? { task: node.task.id } : {}, source)} />}
      </main>
      {divided && <PaneDivider workspace={workspace} pane={pane} width={paneSize} onWidth={setPaneSize} onResizing={setResizing} onDone={(width) => store(PANE_WIDTH_KEY, String(width))} />}
      <aside ref={pane} className="context-pane" hidden={!paneOpen} tabIndex={-1} aria-label={view === "Board" ? "Task review" : "Goblin panel"}>
        {snapshot && cardStart && paneOpen && (!showsPanel
          ? <><PanelRow {...row} />
            <section className="review-placeholder"><Avatar persona="reviewer" /><h2>Review the work</h2><p>Select a task to see what it is doing and what changed.</p></section></>
          : <GoblinPanel key={selectionEpoch + ":" + (selectedSession?.id || task?.id || "cfo") + ":" + (task?.generation || "")}
            task={selected ? task : undefined} node={selected ? selectedSession : undefined} snapshot={snapshot} connected={connected} reviews={reviews}
            view={shownView} now={now} presentations={presentations} cardStart={cardStart} onView={setPanelView} row={row} onAnswer={(key) => setCommandFocus({ key, at: Date.now() })}
            onOpenTask={(next) => select({ task: next.id }, pane.current || document.body)} />)}
        {snapshot && terminalOpened && <Suspense fallback={terminalShown ? <div className="terminal-deck"><div className="deck-stage"><div className="terminal-cover" role="status"><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the terminal</p></div></div></div> : null}>
          <TerminalDeck snapshot={snapshot} task={selected ? task : undefined} node={selected ? selectedSession : undefined} cfo={cfoShown} shown={terminalShown} connected={connected} focus={switchFocus}
            onOwner={task && snapshot.sessions.some((session) => ownsTaskSession(session, task)) ? () => { setSelected({ task: task.id }); setSelectionEpoch((epoch) => epoch + 1); } : undefined} />
        </Suspense>}
      </aside>
    </div>}
  </div></AfkBoard>;
}
