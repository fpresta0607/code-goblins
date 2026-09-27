import { lazy, Suspense, useCallback, useEffect, useRef, useState, type CSSProperties } from "react";
import { useRuntimeStream } from "./stream";
import { Lineage, type Selection } from "./Lineage";
import { Board } from "./Board";
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
import { CFO_KEY, MAXIMIZED_KEYS, maximizedFor, maximizedView, paneTrack, switchOrder } from "./terminalOrder";
import { useSwitchKeys } from "./useSwitchKeys";
import { unsentComment, updateAction } from "./boardUpdate";
import { windowTarget } from "./terminalWindow";
import { message, request } from "./api";
import { FirstRun } from "./FirstRun";
import { Alerts } from "./Alerts";
import { showsFirstRun, type FirstRunChoice } from "./firstRunStart";

// The terminals load xterm, so the deck arrives the first time one is shown.
const TerminalDeck = lazy(() => import("./TerminalDeck").then((module) => ({ default: module.TerminalDeck })));

const PANE_WIDTH_KEY = "cfo-pane-width";

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

export function App() {
  const { snapshot, connection, error } = useRuntimeStream();
  const [view, setView] = useState<"Board" | "Orchestration">("Board");
  // Board opens a goblin on its task view, Orchestration on its terminal.
  const [panelView, setPanelView] = useState<PanelView>("task");
  const [commandFocus, setCommandFocus] = useState<CommandFocus | null>(null);
  const [selected, setSelected] = useState<Selection | null>(null);
  // The CFO chosen by name, which the Board shows in the panel too.
  const [cfoOpen, setCfoOpen] = useState(false);
  const [selectionEpoch, setSelectionEpoch] = useState(0);
  const [paneOpen, setPaneOpen] = useState(true);
  const [paneSize, setPaneSize] = useState<number | null>(() => Number(stored(PANE_WIDTH_KEY)) || null);
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
  const returnFocus = useRef<HTMLElement | null>(null);
  const pane = useRef<HTMLElement>(null);
  const workspace = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const query = matchMedia("(max-width: 40rem)");
    const changed = () => setCompact(query.matches);
    query.addEventListener("change", changed);
    return () => query.removeEventListener("change", changed);
  }, []);
  const node = snapshot?.sessions.find((node) => node.id === selected?.session);
  const task = snapshot?.tasks.find((task) => task.id === (selected?.task || node?.task_id));
  const selectedSession = node || (task && snapshot?.sessions.find((session) => ownsTaskSession(session, task)));
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
    const cfo = !next.session && !next.task || snapshot?.sessions.find((session) => session.id === next.session)?.role === "cfo";
    setSelected(cfo ? null : next);
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
  // hands it the keyboard.
  const switchTo = (key: string) => {
    if (key === CFO_KEY) { setSelected(null); setCfoOpen(true); } else { setSelected({ task: key }); setCfoOpen(false); }
    setPanelView("terminal");
    setPaneOpen(true);
    setSwitchFocus((prior) => prior + 1);
  };
  if (firstRunChoice === "started" && snapshot?.cfo_runs) setFirstRunChoice("");
  // A CFO starting in its terminal waits there for Claude Code's sign-in, so
  // the board opens that terminal by itself, once.
  if (snapshot?.cfo_starting && !startingShown) { setStartingShown(true); setView("Board"); switchTo(CFO_KEY); }
  // The board's root is the first-run page whenever no CFO runs.
  const firstRun = !!snapshot && showsFirstRun({ cfoRuns: snapshot.cfo_runs, choice: firstRunChoice });
  const close = () => {
    setPaneOpen(false);
    requestAnimationFrame(() => returnFocus.current?.isConnected && returnFocus.current.focus());
  };
  const cfoShown = !selected && (view === "Orchestration" || cfoOpen);
  const showsPanel = !!snapshot && paneOpen && (view === "Orchestration" || !!task || cfoOpen);
  const terminalShown = showsPanel && panelView === "terminal";
  if (terminalShown && !terminalOpened) setTerminalOpened(true);
  useSwitchKeys(snapshot ? switchOrder(snapshot.tasks) : [], cfoShown ? CFO_KEY : task?.id || "", switchTo);
  const maximizeView = maximizedView(view, panelView);
  const maximized = maximizedFor(maximizeView, maximizedChoice[maximizeView]);
  const panelWide = paneOpen && maximized && !compact;
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
  const closeButton = <>
    {shownWindow && <button className="icon-button" aria-label="Open in Windows Terminal" data-tip="Open in terminal" data-tip-align="end" onClick={() => void openWindow()}><Icon name="external" /></button>}
    {shownWindow && windowError.shown === shownKey && <p className="window-error" role="alert">{windowError.text}</p>}
    {!compact && <button className="icon-button" aria-label={maximized ? "Restore the panel" : "Maximize the panel"} data-tip={maximized ? "Restore" : "Maximize"} data-tip-align="end" onClick={() => { const choice = String(!maximized); setMaximizedChoice((prior) => ({ ...prior, [maximizeView]: choice })); store(MAXIMIZED_KEYS[maximizeView], choice); }}><Icon name={maximized ? "restore" : "maximize"} /></button>}
    <button className="icon-button" aria-label="Close panel" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>
  </>;
  return <div className="app-shell" onKeyDown={(event) => {
    if (event.key === "Escape" && paneOpen && !event.defaultPrevented) { event.preventDefault(); close(); }
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
        {snapshot && <CommandCenter snapshot={snapshot} connected={connected} presentations={presentations} focus={commandFocus} onUnsent={onUnsent} />}
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
    </main> : <div ref={workspace} className={"workspace" + (paneOpen ? " with-pane" : "")} style={layout}>
      <main className="canvas-region" aria-label={view} hidden={panelWide}>
        {(error || snapshot?.error) && <div className="connection-banner" role="alert">{error || snapshot?.error}</div>}
        {snapshot?.registration && <div className="connection-banner" role="alert">{snapshot.registration}</div>}
        {!snapshot ? <div className="empty-state" role="status"><h2>Connecting to the supervisor</h2><p>Loading tasks and native sessions.</p></div>
          : view === "Board" ? <Board presentations={presentations} snapshot={snapshot} selected={task?.id} onSelect={(task, source) => select({ task: task.id }, source)} onTerminal={(task, source) => select({ task: task.id }, source, "terminal")} onOpenCfo={(source) => { returnFocus.current = source; switchTo(CFO_KEY); }} onStartCfo={() => setFirstRunChoice("")} />
            : compact ? <Lineage presentations={presentations} effects={effects} snapshot={snapshot} project="" selected={selectedSession ? { session: selectedSession.id } : selected} onSelect={select} />
              : <Orchestration presentations={presentations} effects={effects} snapshot={snapshot} connected={connected} selected={selectedSession ? "session:" + selectedSession.id : selected?.task ? "task:" + selected.task : ""}
                onSelect={(node, source) => select(node.session ? { session: node.session.id } : node.task ? { task: node.task.id } : {}, source)} />}
      </main>
      {paneOpen && !panelWide && !compact && <PaneDivider workspace={workspace} pane={pane} width={paneSize} onWidth={setPaneSize} onDone={(width) => store(PANE_WIDTH_KEY, String(width))} />}
      <aside ref={pane} className="context-pane" hidden={!paneOpen} tabIndex={-1} aria-label={view === "Board" ? "Task review" : "Goblin panel"}>
        {snapshot && paneOpen && (!showsPanel
          ? <><div className="panel-top"><div className="panel-top-side" /><div /><div className="panel-top-side end">{closeButton}</div></div>
            <section className="review-placeholder"><Avatar persona="reviewer" /><h2>Review the work</h2><p>Select a task to see what it is doing and what changed.</p></section></>
          : <GoblinPanel key={selectionEpoch + ":" + (selectedSession?.id || task?.id || "cfo") + ":" + (task?.generation || "")}
            task={selected ? task : undefined} node={selected ? selectedSession : undefined} snapshot={snapshot} connected={connected} reviews={reviews}
            view={panelView} onView={setPanelView} trailing={closeButton} onAnswer={(key) => setCommandFocus({ key, at: Date.now() })}
            onOpenTask={(next) => select({ task: next.id }, pane.current || document.body)}
            leading={view === "Orchestration" && selected ? <button className="icon-button" aria-label="Back to CFO" data-tip="Back to CFO" data-tip-align="start" onClick={() => setSelected(null)}><Icon name="back" /></button> : undefined} />)}
        {snapshot && terminalOpened && <Suspense fallback={terminalShown ? <div className="terminal-deck"><div className="deck-stage"><div className="terminal-cover" role="status"><span className="terminal-spinner" aria-hidden="true" /><p>Connecting to the terminal</p></div></div></div> : null}>
          <TerminalDeck snapshot={snapshot} task={selected ? task : undefined} node={selected ? selectedSession : undefined} cfo={cfoShown} shown={terminalShown} connected={connected} focus={switchFocus}
            onOwner={task && snapshot.sessions.some((session) => ownsTaskSession(session, task)) ? () => { setSelected({ task: task.id }); setSelectionEpoch((epoch) => epoch + 1); } : undefined} />
        </Suspense>}
      </aside>
    </div>}
  </div>;
}
