import { useEffect, useMemo, useRef, useState, type PointerEvent } from "react";
import type { BoardActivity, Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { activityDisplay, presentationShownOn } from "./activity";
import { Chevron } from "./Chevron";
import { Icon } from "./Icon";
import { ownsTaskSession } from "./lineageTree";
import { arrange, asksOverlord, expireTraffic, fitScale, fleetTraffic, NODE_HEIGHT, NODE_WIDTH, nodeStatus, personaFor, PULSE_MS, reportTraffic, workflowNodes, type Point, type WorkflowNode } from "./workflow";

const layoutKey = "cfo-orchestration-layout-v1";

function readLayout(): { positions: Record<string, Point>; error: string } {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(layoutKey) || "{}");
    if (typeof saved !== "object" || saved === null || Array.isArray(saved)) throw new Error();
    const positions: Record<string, Point> = {};
    for (const [id, point] of Object.entries(saved)) {
      if (Object.keys(positions).length >= 512) break;
      if (typeof point !== "object" || !point || !("x" in point) || !("y" in point)
        || typeof point.x !== "number" || typeof point.y !== "number"
        || !Number.isFinite(point.x) || !Number.isFinite(point.y)
        || point.x < 0 || point.y < 0 || point.x > 50000 || point.y > 50000) throw new Error();
      positions[id] = { x: point.x, y: point.y };
    }
    return { positions, error: "" };
  } catch { return { positions: {}, error: "Saved layout is unavailable. Arrange starts a fresh layout." }; }
}

export function Orchestration({ snapshot, selected, connected, effects, onSelect, presentations }: {
  presentations:BoardActivity[];
  snapshot: Snapshot; selected: string; connected: boolean; effects: BoardActivity[];
  onSelect: (node: WorkflowNode, source: HTMLElement) => void;
}) {
  const nodes = useMemo(() => workflowNodes(snapshot), [snapshot]);
  const automatic = useMemo(() => arrange(nodes), [nodes]);
  const [layout, setLayout] = useState(readLayout);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // The graph fills the visible canvas, centered, until a zoom or a pan by
  // hand takes over; Fit hands it back. A dragged card holds the scale still.
  const [canvas, setCanvas] = useState({ width: 0, height: 0 });
  const [manual, setManual] = useState<number | null>(null);
  const [held, setHeld] = useState<number | null>(null);

  const viewport = useRef<HTMLDivElement>(null);
  const ignoreClick = useRef(false);
  const drag = useRef<{ id: string; pointer: Point; start: Point } | null>(null);
  const pan = useRef<{ pointer: Point; scroll: Point } | null>(null);
  const signatures = useRef<Map<string, string> | null>(null);
  const [traffic, setTraffic] = useState<Record<string, number>>({});
  const pulses = useRef(new Set<ReturnType<typeof setTimeout>>());
  useEffect(() => {
    const { signatures: next, moved } = fleetTraffic(signatures.current, snapshot);
    signatures.current = next;
    if (!moved.length) return;
    const at = Date.now();
    setTraffic((prior) => reportTraffic(prior, moved, at));
    const timer = setTimeout(() => {
      pulses.current.delete(timer);
      setTraffic((prior) => expireTraffic(prior, moved, at));
    }, PULSE_MS);
    pulses.current.add(timer);
  }, [snapshot]);
  useEffect(() => {
    const pending = pulses.current;
    return () => pending.forEach(clearTimeout);
  }, []);
  const byID = new Map(nodes.map((node) => [node.id, node]));
  const hidden = (node: WorkflowNode) => {
    const seen = new Set([node.id]);
    let parent = node.parent;
    while (parent && !seen.has(parent)) {
      if (collapsed.has(parent)) return true;
      seen.add(parent);
      parent = byID.get(parent)?.parent;
    }
    return false;
  };
  const visible = nodes.filter((node) => !hidden(node));
  const point = (id: string) => layout.positions[id] || automatic[id];
  const xs = visible.map((node) => point(node.id).x), ys = visible.map((node) => point(node.id).y);
  const left = xs.length ? Math.max(0, Math.min(...xs) - 40) : 0;
  const top = ys.length ? Math.max(0, Math.min(...ys) - 40) : 0;
  const width = Math.max(1, ...xs.map((x) => x + NODE_WIDTH + 40)) - left;
  const height = Math.max(1, ...ys.map((y) => y + NODE_HEIGHT + 80)) - top;
  const scale = manual ?? held ?? fitScale({ width, height }, canvas);
  const zoom = (next: number) => setManual(Math.max(.35, Math.min(1.5, next)));
  const fit = () => { setManual(null); viewport.current?.scrollTo(0, 0); };
  useEffect(() => {
    const element = viewport.current;
    if (!element) return;
    const observer = new ResizeObserver(() => setCanvas({ width: element.clientWidth, height: element.clientHeight }));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const move = (id: string, next: Point) => setLayout((prior) => ({ ...prior, positions: {
    ...prior.positions, [id]: { x: Math.max(0, Math.min(50000, next.x)), y: Math.max(0, Math.min(50000, next.y)) },
  } }));
  const save = () => {
    try {
      const retained = Object.fromEntries(nodes.map((node) => [node.id, point(node.id)]));
      localStorage.setItem(layoutKey, JSON.stringify(retained));
    } catch { setLayout((prior) => ({ ...prior, error: "Layout could not be saved in this browser." })); }
  };
  const startDrag = (event: PointerEvent<HTMLButtonElement>, node: WorkflowNode) => {
    if (event.button !== 0) return;
    event.stopPropagation();
    event.currentTarget.setPointerCapture(event.pointerId);
    drag.current = { id: node.id, pointer: { x: event.clientX, y: event.clientY }, start: point(node.id) };
    setHeld(scale);
    ignoreClick.current = false;
  };
  const moveDrag = (event: PointerEvent<HTMLButtonElement>) => {
    if (!drag.current) return;
    const { id, pointer, start } = drag.current;
    const dx = event.clientX - pointer.x, dy = event.clientY - pointer.y;
    if (Math.abs(dx) + Math.abs(dy) < 4 && !ignoreClick.current) return;
    ignoreClick.current = true;
    move(id, { x: start.x + dx / scale, y: start.y + dy / scale });
  };
  const endDrag = () => { if (drag.current) save(); drag.current = null; setHeld(null); };
  return <section className="orchestration" aria-label="Orchestration">
    <p className="sr-only" id="canvas-help">Drag a card to reposition it. With a card focused, Alt and arrow keys move it, Shift moves farther. On the canvas, plus and minus zoom, zero fits. Moving cards never changes parent relationships.</p>
    <div ref={viewport} className="flow-canvas" tabIndex={0} aria-label="Connected family tree" aria-describedby="canvas-help"
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key === "+" || event.key === "=") { event.preventDefault(); zoom(scale + .1); }
        if (event.key === "-") { event.preventDefault(); zoom(scale - .1); }
        if (event.key === "0") { event.preventDefault(); fit(); }
      }}
      onPointerDown={(event) => {
        if (event.button !== 0 || !viewport.current || (event.target instanceof Element && event.target.closest(".flow-node, button"))) return;
        event.currentTarget.setPointerCapture(event.pointerId);
        pan.current = { pointer: { x: event.clientX, y: event.clientY }, scroll: { x: viewport.current.scrollLeft, y: viewport.current.scrollTop } };
      }}
      onPointerMove={(event) => {
        const element = viewport.current;
        if (!pan.current || !element) return;
        const before = element.scrollLeft + element.scrollTop;
        element.scrollTo(pan.current.scroll.x - event.clientX + pan.current.pointer.x, pan.current.scroll.y - event.clientY + pan.current.pointer.y);
        // Only a pan that actually moved the view takes over from auto-fit.
        if (manual === null && element.scrollLeft + element.scrollTop !== before) setManual(scale);
      }}
      onPointerUp={() => { pan.current = null; }} onPointerCancel={() => { pan.current = null; }}>
      {!nodes.length && <div className="empty-state"><h2>No sessions reported yet</h2><p>Native parent relationships will appear here as work starts.</p></div>}
      <div className="flow-space" style={{ width: width * scale, height: height * scale }}>
        <div className="flow-world" style={{ width, height, transform: `scale(${scale}) translate(${-left}px, ${-top}px)` }}>
          <svg className="flow-connections" width={left + width} height={top + height} aria-hidden="true">
            {visible.map((node) => {
              if (!node.parent || !visible.some((other) => other.id === node.parent)) return null;
              const from = point(node.parent), to = point(node.id);
              // A native cycle stays labeled in details and visible in layout;
              // drawing it as normal downward ancestry would be misleading.
              const seen = new Set([node.id]);
              let parent: string | undefined = node.parent;
              while (parent && !seen.has(parent)) { seen.add(parent); parent = byID.get(parent)?.parent; }
              if (parent) return null;
              const sx = from.x + NODE_WIDTH / 2, sy = from.y + NODE_HEIGHT;
              const ex = to.x + NODE_WIDTH / 2, ey = to.y;
              // Travel sideways just below the parent, then drop straight
              // down, so a connector to a second row passes through a gap in
              // the first. For the next row this is the plain S curve.
              const drop = Math.min((ey - sy) / 2, 56);
              const path = `M${sx},${sy} C${sx},${sy + drop} ${ex},${sy + drop} ${ex},${sy + 2 * drop} L${ex},${ey}`;
              const activity = activityDisplay(connected ? effects : [],node.session?.id || "",byID.get(node.parent || "")?.session?.id || "");
              const report = connected && node.task ? traffic[node.task.id] : undefined;
              const communicating = activity.communication || report;
              return <g key={node.id + ":" + (activity.communication?.id || activity.creation?.id || report || "")} className={"connection-line " + (communicating ? "communicating " : "") + (activity.creation ? "creating " : "") + "relation-" + node.relation}>
                <path d={path} /><circle cx={sx} cy={sy} r={4} /><circle cx={ex} cy={ey} r={4} />
                {communicating && <path className="communication-pulse" d={path} />}
              </g>;
            })}
          </svg>
          {visible.map((node) => {
            const parent = node.parent ? byID.get(node.parent) : undefined;
            const activity = activityDisplay(connected ? effects : [],node.session?.id || "",parent?.session?.id || "");
            const effect = activity.received || activity.created;
            const p = point(node.id), owner = ownsTaskSession(node.session, node.task);
            const phase = owner ? node.task?.phase : node.session?.runtime?.state || node.session?.phase;
            const children = nodes.some((child) => child.parent === node.id);
            const asking = owner && asksOverlord(snapshot, node.task?.id || ""), status = nodeStatus(node, asking);
            return <article key={node.id} className={"flow-node" + (activity.created ? " node-enter" : "") + (selected === node.id ? " selected" : "")} style={{ left: p.x, top: p.y, width: NODE_WIDTH, height: NODE_HEIGHT }}>
              {effect && <span key={effect.id} className="activity-glow" aria-hidden="true" />}
              <button className="flow-node-main" onPointerDown={(event) => startDrag(event, node)} onPointerMove={moveDrag} onPointerUp={endDrag} onPointerCancel={endDrag}
                onKeyDown={(event) => {
                  if (!event.altKey || !event.key.startsWith("Arrow")) return;
                  event.preventDefault();
                  const step = event.shiftKey ? 50 : 10;
                  move(node.id, { x: p.x + (event.key === "ArrowRight" ? step : event.key === "ArrowLeft" ? -step : 0), y: p.y + (event.key === "ArrowDown" ? step : event.key === "ArrowUp" ? -step : 0) });
                }} onKeyUp={(event) => { if (event.key.startsWith("Arrow")) save(); }}
                onClick={(event) => { if (ignoreClick.current) { ignoreClick.current = false; return; } onSelect(node, event.currentTarget); }} aria-pressed={selected === node.id}
                aria-label={node.title + ". " + status + ". " + (parent ? "Parent: " + parent.title : node.relation)} aria-describedby="canvas-help">
                <Avatar persona={node.cfo ? "cfo" : personaFor(node.task, node.session)} />
                <span className="card-copy"><strong>{node.title}</strong>{presentations.some(a=>presentationShownOn(a,node.session,node.task))&&<span className="browser-indicator">Browser active</span>}{node.task?.project && <span className="project-label">{node.task.project}</span>}<span className={"plain-status phase-" + phase + (asking ? " asking" : "")}><span className="status-dot" />{status}</span>
                  {!node.parent && node.session?.role !== "cfo" && !node.cfo && <small>{node.relation}</small>}
                </span>
              </button>
              {children && <button className="node-disclosure" aria-label={(collapsed.has(node.id) ? "Expand" : "Collapse") + " descendants of " + node.title} aria-expanded={!collapsed.has(node.id)} onClick={() => setCollapsed((prior) => { const next = new Set(prior); if (next.has(node.id)) next.delete(node.id); else next.add(node.id); return next; })}><Chevron collapsed={collapsed.has(node.id)} /></button>}
            </article>;
          })}
        </div>
      </div>
    </div>
    <div className="canvas-controls">
      <button onClick={() => {
        setLayout({ positions: {}, error: "" }); setCollapsed(new Set());
        try { localStorage.removeItem(layoutKey); }
        catch { setLayout({ positions: {}, error: "Layout could not be cleared in this browser." }); }
      }} className="icon-button" aria-label="Arrange" data-tip="Arrange" data-tip-align="start"><Icon name="arrange" /></button>
      <div><button className="icon-button" aria-label="Zoom out" data-tip="Zoom out" onClick={() => zoom(scale - .1)}><Icon name="minus" /></button><output aria-label="Zoom">{Math.round(scale * 100)}%</output><button className="icon-button" aria-label="Zoom in" data-tip="Zoom in" onClick={() => zoom(scale + .1)}><Icon name="plus" /></button><button className="icon-button" aria-label="Fit canvas" data-tip="Fit" data-tip-align="end" onClick={fit}><Icon name="fit" /></button></div>
    </div>
    {layout.error && <p className="layout-notice" role="status">{layout.error}</p>}
  </section>;
}
