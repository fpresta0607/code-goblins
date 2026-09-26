import { lazy, Suspense, useEffect, useRef, useState, type CSSProperties } from "react";
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
import { CFO_KEY, paneTrack, switchOrder } from "./terminalOrder";
import { useSwitchKeys } from "./useSwitchKeys";
import { updateAction } from "./boardUpdate";

// The terminals load xterm, so the deck arrives the first time one is shown.
const TerminalDeck = lazy(() => import("./TerminalDeck").then((module) => ({ default: module.TerminalDeck })));

const PANE_WIDTH_KEY = "cfo-pane-width";
const PANE_MAXIMIZED_KEY = "cfo-pane-maximized";

// The board build this page was loaded with, which the supervisor names in
// the page; a tab left open across an install keeps the older one.
const LOADED_BUILD = document.querySelector<HTMLMetaElement>('meta[name="cfo-build"]')?.content || "";
// The Overlord is in the middle of an answer while the Command Center is open
// or any text field holds text he typed.
const answering = () => !!document.querySelector("dialog[open]")
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
  const [maximized, setMaximized] = useState(() => stored(PANE_MAXIMIZED_KEY) === "true");
  const [terminalOpened, setTerminalOpened] = useState(false);
  const [switchFocus, setSwitchFocus] = useState(0);
  const [compact, setCompact] = useState(() => matchMedia("(max-width: 40rem)").matches);
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
  const updated = updateAction({ loaded: LOADED_BUILD, served, hidden: false, answering: true }) !== "none";
  useEffect(() => {
    const check = () => { if (updateAction({ loaded: LOADED_BUILD, served, hidden: document.hidden, answering: answering() }) === "reload") location.reload(); };
    check();
    document.addEventListener("visibilitychange", check);
    return () => document.removeEventListener("visibilitychange", check);
  }, [served]);
  const connected = connection === "Live";
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
  // A switch from the switcher or its keys shows that terminal at once and
  // hands it the keyboard.
  const switchTo = (key: string) => {
    if (key === CFO_KEY) { setSelected(null); setCfoOpen(true); } else { setSelected({ task: key }); setCfoOpen(false); }
    setPanelView("terminal");
    setPaneOpen(true);
    setSwitchFocus((prior) => prior + 1);
  };
  const close = () => {
    setPaneOpen(false);
    requestAnimationFrame(() => returnFocus.current?.isConnected && returnFocus.current.focus());
  };
  const cfoShown = !selected && (view === "Orchestration" || cfoOpen);
  const showsPanel = !!snapshot && paneOpen && (view === "Orchestration" || !!task || cfoOpen);
  const terminalShown = showsPanel && panelView === "terminal";
  if (terminalShown && !terminalOpened) setTerminalOpened(true);
  useSwitchKeys(snapshot ? switchOrder(snapshot.tasks) : [], cfoShown ? CFO_KEY : task?.id || "", switchTo);
  const panelWide = paneOpen && maximized && !compact;
  const layout: CSSProperties | undefined = panelWide ? { gridTemplateColumns: "minmax(0, 1fr)" } : paneOpen && paneSize && !compact ? { gridTemplateColumns: `minmax(0, 1fr) 10px ${paneTrack(paneSize)}` } : undefined;
  const closeButton = <>
    {!compact && <button className="icon-button" aria-label={maximized ? "Restore the panel" : "Maximize the panel"} data-tip={maximized ? "Restore" : "Maximize"} data-tip-align="end" onClick={() => { setMaximized(!maximized); store(PANE_MAXIMIZED_KEY, String(!maximized)); }}><Icon name={maximized ? "restore" : "maximize"} /></button>}
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
        {(["Board", "Orchestration"] as const).map((name) => <button key={name} aria-pressed={view === name} onClick={() => { setView(name); setPanelView(name === "Board" ? "task" : "terminal"); }}>{name}</button>)}
      </div>
      <div className="topbar-controls">
        {snapshot && <CommandCenter snapshot={snapshot} connected={connected} presentations={presentations} focus={commandFocus} />}
        <div className="connection" role="status">
          <span className={"live-dot " + (!connected ? "offline" : "")} />{connection}
        </div>
        {!paneOpen && <button onClick={() => setPaneOpen(true)}>Open {selected ? "details" : "CFO"}</button>}
      </div>
    </header>
    {updated && <div className="update-banner" role="status"><span>The board was updated.</span><button className="primary" onClick={() => location.reload()}>Reload</button></div>}
    <div ref={workspace} className={"workspace" + (paneOpen ? " with-pane" : "")} style={layout}>
      <main className="canvas-region" aria-label={view} hidden={panelWide}>
        {(error || snapshot?.error) && <div className="connection-banner" role="alert">{error || snapshot?.error}</div>}
        {snapshot?.registration && <div className="connection-banner" role="alert">{snapshot.registration}</div>}
        {!snapshot ? <div className="empty-state" role="status"><h2>Connecting to the supervisor</h2><p>Loading tasks and native sessions.</p></div>
          : view === "Board" ? <Board presentations={presentations} snapshot={snapshot} selected={task?.id} onSelect={(task, source) => select({ task: task.id }, source)} onTerminal={(task, source) => select({ task: task.id }, source, "terminal")} onOpenCfo={(source) => { returnFocus.current = source; switchTo(CFO_KEY); }} />
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
            onSwitch={switchTo} onOwner={task && snapshot.sessions.some((session) => ownsTaskSession(session, task)) ? () => { setSelected({ task: task.id }); setSelectionEpoch((epoch) => epoch + 1); } : undefined} />
        </Suspense>}
      </aside>
    </div>
  </div>;
}
