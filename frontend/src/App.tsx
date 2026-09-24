import { useEffect, useRef, useState } from "react";
import { useRuntimeStream } from "./stream";
import { Lineage, type Selection } from "./Lineage";
import { Board } from "./Board";
import { Orchestration } from "./Orchestration";
import { Questions } from "./Questions";
import { useActivity } from "./useActivity";
import { livePresentations } from "./activity";
import { useReview } from "./review";
import { ownsTaskSession } from "./lineageTree";
import { Icon } from "./Icon";
import { Avatar } from "./Avatar";
import { GoblinPanel, type PanelView } from "./GoblinPanel";

export function App() {
  const { snapshot, connection, error } = useRuntimeStream();
  const [view, setView] = useState<"Board" | "Orchestration">("Board");
  // Board opens a goblin on its task view, Orchestration on its terminal.
  const [panelView, setPanelView] = useState<PanelView>("task");
  const [selected, setSelected] = useState<Selection | null>(null);
  const [selectionEpoch, setSelectionEpoch] = useState(0);
  const [paneOpen, setPaneOpen] = useState(true);
  const [compact, setCompact] = useState(() => matchMedia("(max-width: 40rem)").matches);
  const returnFocus = useRef<HTMLElement | null>(null);
  const pane = useRef<HTMLElement>(null);
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
  const connected = connection === "Live";
  const effects = useActivity(snapshot, connected);
  const [now,setNow]=useState(Date.now);
  useEffect(()=>{const timer=setInterval(()=>setNow(Date.now()),1000);return()=>clearInterval(timer);},[]);
  const presentations=snapshot&&connected?livePresentations(snapshot,now):[];
  const select = (next: Selection, source: HTMLElement) => {
    returnFocus.current = source;
    // An empty selection is the supervisor root drawn for the CFO.
    const cfo = !next.session && !next.task || snapshot?.sessions.find((session) => session.id === next.session)?.role === "cfo";
    setSelected(cfo ? null : next);
    setSelectionEpoch((epoch) => epoch + 1);
    setPaneOpen(true);
    requestAnimationFrame(() => {
      pane.current?.focus({ preventScroll: true });
      if (compact) pane.current?.scrollIntoView({ behavior: "instant", block: "start" });
    });
  };
  const close = () => {
    setPaneOpen(false);
    requestAnimationFrame(() => returnFocus.current?.isConnected && returnFocus.current.focus());
  };
  const closeButton = <button className="icon-button" aria-label="Close panel" data-tip="Close" data-tip-align="end" onClick={close}><Icon name="close" /></button>;
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
        {snapshot && <Questions snapshot={snapshot} connected={connected} presentations={presentations} />}
        <div className="connection" role="status">
          <span className={"live-dot " + (!connected ? "offline" : "")} />{connection}
        </div>
        {!paneOpen && <button onClick={() => setPaneOpen(true)}>Open {selected ? "details" : "CFO"}</button>}
      </div>
    </header>
    <div className={"workspace" + (paneOpen ? " with-pane" : "")}>
      <main className="canvas-region" aria-label={view}>
        {(error || snapshot?.error) && <div className="connection-banner" role="alert">{error || snapshot?.error}</div>}
        {snapshot?.registration && <div className="connection-banner" role="alert">{snapshot.registration}</div>}
        {!snapshot ? <div className="empty-state" role="status"><h2>Connecting to the supervisor</h2><p>Loading tasks and native sessions.</p></div>
          : view === "Board" ? <Board presentations={presentations} snapshot={snapshot} selected={task?.id} onSelect={(task, source) => select({ task: task.id }, source)} />
            : compact ? <Lineage presentations={presentations} effects={effects} snapshot={snapshot} project="" selected={selectedSession ? { session: selectedSession.id } : selected} onSelect={select} />
              : <Orchestration presentations={presentations} effects={effects} snapshot={snapshot} connected={connected} selected={selectedSession ? "session:" + selectedSession.id : selected?.task ? "task:" + selected.task : ""}
                onSelect={(node, source) => select(node.session ? { session: node.session.id } : node.task ? { task: node.task.id } : {}, source)} />}
      </main>
      <aside ref={pane} className="context-pane" hidden={!paneOpen} tabIndex={-1} aria-label={view === "Board" ? "Task review" : "Goblin panel"}>
        {snapshot && paneOpen && (view === "Board" && !task
          ? <><div className="panel-top"><div className="panel-top-side" /><div /><div className="panel-top-side end">{closeButton}</div></div>
            <section className="review-placeholder"><Avatar persona="reviewer" /><h2>Review the work</h2><p>Select a task to see what it is doing and what changed.</p></section></>
          : <GoblinPanel key={selectionEpoch + ":" + (selectedSession?.id || task?.id || "cfo") + ":" + (task?.generation || "")}
            task={selected ? task : undefined} node={selected ? selectedSession : undefined} snapshot={snapshot} connected={connected} reviews={reviews}
            view={panelView} onView={setPanelView} trailing={closeButton}
            leading={view === "Orchestration" && selected ? <button className="icon-button" aria-label="Back to CFO" data-tip="Back to CFO" data-tip-align="start" onClick={() => setSelected(null)}><Icon name="back" /></button> : undefined}
            onOwner={task && snapshot.sessions.some((session) => ownsTaskSession(session, task)) ? () => { setSelected({ task: task.id }); setSelectionEpoch((epoch) => epoch + 1); } : undefined} />)}
      </aside>
    </div>
  </div>;
}
