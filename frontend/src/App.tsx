import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { useRuntimeStream } from "./stream";
import { Lineage, type Selection } from "./Lineage";
import { Board } from "./Board";
import { Orchestration } from "./Orchestration";
import { Details } from "./Details";
import { Questions } from "./Questions";
import { useActivity } from "./useActivity";
import { livePresentations } from "./activity";
import { useReview } from "./review";
import { ownsTaskSession } from "./lineageTree";

const NativeTerminal = lazy(() => import("./NativeTerminal").then((module) => ({ default: module.NativeTerminal })));

export function App() {
  const { snapshot, connection, error } = useRuntimeStream();
  const [view, setView] = useState<"Board" | "Orchestration">("Board");
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
  return <div className="app-shell" onKeyDown={(event) => {
    if (event.key === "Escape" && paneOpen && !event.defaultPrevented) { event.preventDefault(); close(); }
  }}>
    <header className="topbar">
      <a className="brand" href="/" aria-label="Code Goblins home">
        <img src="/assets/goblin-app.png" width="48" height="48" alt="" /><h1>Code Goblins</h1>
        {snapshot?.example && <span className="example-label">Example workspace</span>}
      </a>
      <div className="view-switch" role="group" aria-label="Workspace view">
        {(["Board", "Orchestration"] as const).map((name) => <button key={name} aria-pressed={view === name} onClick={() => setView(name)}>{name}</button>)}
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
      <aside ref={pane} className="context-pane" hidden={!paneOpen} tabIndex={-1} aria-label={view === "Board" ? "Task review" : "Native terminal"}>
        <div className="pane-controls">
          {view === "Orchestration" && selected ? <button className="return-cfo" onClick={() => setSelected(null)}>Back to CFO</button> : <span className="muted">{view === "Board" ? "Review" : "CFO terminal"}</span>}
          <button className="icon-button" aria-label="Close contextual pane" onClick={close}>×</button>
        </div>
        {snapshot && (view === "Board"
          ? <Details key={selectionEpoch} task={task} snapshot={snapshot} connected={connected} reviews={reviews} />
          : paneOpen && <Suspense fallback={<p className="loading">Opening native terminal...</p>}><NativeTerminal key={(selectedSession?.id || task?.id || "cfo") + ":" + (task?.generation || "")} task={selected ? task : undefined} node={selected ? selectedSession : undefined} instance={snapshot.instance} visible={connected} onOwner={task && snapshot.sessions.some((session) => ownsTaskSession(session, task)) ? () => { setSelected({ task: task.id }); setSelectionEpoch((epoch) => epoch + 1); } : undefined} /></Suspense>)}
      </aside>
    </div>
  </div>;
}
