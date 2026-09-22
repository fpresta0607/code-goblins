import { useEffect, useRef, useState } from "react";
import { useRuntimeStream } from "./stream";
import { Lineage, type Selection } from "./Lineage";
import { Board } from "./Board";
import { Orchestration } from "./Orchestration";
import { Details } from "./Details";
import { CFOPane } from "./CFOPane";
import { useMessages } from "./Messages";
import { useReview } from "./review";
import { age } from "./presentation";
import { decisionText } from "./types";
import { ownsTaskSession } from "./lineageTree";

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
  const messages = useMessages(snapshot);
  const decisions = snapshot?.decisions.filter((decision) => decision.kind !== "heartbeat") || [];
  const connected = connection === "Live";
  const select = (next: Selection, source: HTMLElement) => {
    returnFocus.current = source;
    const cfo = snapshot?.sessions.find((session) => session.id === next.session)?.role === "cfo";
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
        {(decisions.length > 0 || !!snapshot?.issues.length) && <details className="attention-menu">
          <summary>Needs attention <span>{decisions.length + (snapshot?.issues.length || 0)}</span></summary>
          <div className="attention-content">
            {decisions.slice().reverse().map((decision) => <article className="decision" key={decision.seq}>
              <h3>{snapshot?.tasks.find((task) => task.id === decision.key)?.title || decision.key || "Fleet decision"}</h3>
              <p>{decisionText(decision.detail)}</p><time>{age(decision.time)}</time>
            </article>)}
            {!!snapshot?.issues.length && <details><summary>Event diagnostics</summary><ul>{snapshot.issues.map((issue, i) => <li key={i}>{issue}</li>)}</ul></details>}
          </div>
        </details>}
        <div className="connection" role="status" title={snapshot ? "Updated " + age(snapshot.at) : "Waiting for native evidence"}>
          <span className={"live-dot " + (!connected ? "offline" : "")} />{connection}
        </div>
        {!paneOpen && <button onClick={() => setPaneOpen(true)}>Open {selected ? "details" : "CFO"}</button>}
      </div>
    </header>
    <div className={"workspace" + (paneOpen ? " with-pane" : "")}>
      <main className="canvas-region" aria-label={view}>
        {(error || snapshot?.error) && <div className="connection-banner" role="alert">{error || snapshot?.error}</div>}
        {!snapshot ? <div className="empty-state" role="status"><h2>Connecting to the supervisor</h2><p>Loading tasks and native sessions.</p></div>
          : view === "Board" ? <Board snapshot={snapshot} selected={task?.id} onSelect={(task, source) => select({ task: task.id }, source)} />
            : compact ? <Lineage snapshot={snapshot} project="" selected={selectedSession ? { session: selectedSession.id } : selected} onSelect={select} />
              : <Orchestration snapshot={snapshot} connected={connected} selected={selectedSession ? "session:" + selectedSession.id : selected?.task ? "task:" + selected.task : ""}
                onSelect={(node, source) => select(node.session ? { session: node.session.id } : { task: node.task?.id }, source)} />}
      </main>
      <aside ref={pane} className="context-pane" hidden={!paneOpen} tabIndex={-1} aria-label={selected ? "Task details" : "CFO conversation"}>
        <div className="pane-controls">
          {selected ? <button className="return-cfo" onClick={() => setSelected(null)}>← CFO conversation</button> : <span className="muted">Conversation</span>}
          <button className="icon-button" aria-label="Close contextual pane" onClick={close}>×</button>
        </div>
        {snapshot && selected ? <Details key={selectionEpoch} task={task} node={node}
          missingSession={!!selected.session && !node} snapshot={snapshot} connected={connected} visible={paneOpen}
          reviews={reviews} messages={messages} onSelectTask={(id, source) => select({ task: id }, source)} />
          : <CFOPane snapshot={snapshot} messages={messages} connected={connected} visible={paneOpen} />}
      </aside>
    </div>
  </div>;
}
