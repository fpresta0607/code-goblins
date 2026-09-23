import { useEffect, useMemo, useRef, useState, type PointerEvent } from "react";
import type { Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Chevron } from "./Chevron";
import { ownsTaskSession } from "./lineageTree";
import { arrange, NODE_HEIGHT, NODE_WIDTH, nodeStatus, personaFor, recentCommunication, workflowNodes, type Point, type WorkflowNode } from "./workflow";

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

export function Orchestration({ snapshot, selected, connected, onSelect }: {
  snapshot: Snapshot; selected: string; connected: boolean;
  onSelect: (node: WorkflowNode, source: HTMLElement) => void;
}) {
  const nodes = useMemo(() => workflowNodes(snapshot), [snapshot]);
  const automatic = useMemo(() => arrange(nodes), [nodes]);
  const [layout, setLayout] = useState(readLayout);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [scale, setScale] = useState(1);
  const [now, setNow] = useState(Date.now);
  const viewport = useRef<HTMLDivElement>(null);
  const ignoreClick = useRef(false);
  const drag = useRef<{ id: string; pointer: Point; start: Point } | null>(null);
  const pan = useRef<{ pointer: Point; scroll: Point } | null>(null);
  const initialized = useRef(false);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
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
  const width = Math.max(640, ...visible.map((node) => point(node.id).x + NODE_WIDTH + 40));
  const height = Math.max(480, ...visible.map((node) => point(node.id).y + NODE_HEIGHT + 80));
  const fit = () => {
    const canvas = viewport.current;
    if (!canvas) return;
    setScale(Math.max(.35, Math.min(1, (canvas.clientWidth - 24) / width, (canvas.clientHeight - 24) / height)));
    canvas.scrollTo(0, 0);
  };
  useEffect(() => {
    if (initialized.current || !nodes.length || !viewport.current) return;
    const canvas = viewport.current;
    const frame = requestAnimationFrame(() => {
      initialized.current = true;
      setScale(Math.max(.8, Math.min(1, (canvas.clientWidth - 24) / width)));
    });
    return () => cancelAnimationFrame(frame);
  }, [nodes.length, width]);
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
  const endDrag = () => { if (drag.current) save(); drag.current = null; };
  return <section className="orchestration" aria-label="Orchestration">
    <p className="sr-only" id="canvas-help">Drag a card to reposition it. With a card focused, Alt and arrow keys move it, Shift moves farther. On the canvas, plus and minus zoom, zero fits. Moving cards never changes parent relationships.</p>
    <div ref={viewport} className="flow-canvas" tabIndex={0} aria-label="Connected family tree" aria-describedby="canvas-help"
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key === "+" || event.key === "=") { event.preventDefault(); setScale(Math.min(1.5, scale + .1)); }
        if (event.key === "-") { event.preventDefault(); setScale(Math.max(.35, scale - .1)); }
        if (event.key === "0") { event.preventDefault(); fit(); }
      }}
      onPointerDown={(event) => {
        if (event.button !== 0 || !viewport.current || (event.target instanceof Element && event.target.closest(".flow-node, button"))) return;
        event.currentTarget.setPointerCapture(event.pointerId);
        pan.current = { pointer: { x: event.clientX, y: event.clientY }, scroll: { x: viewport.current.scrollLeft, y: viewport.current.scrollTop } };
      }}
      onPointerMove={(event) => {
        if (pan.current && viewport.current) viewport.current.scrollTo(pan.current.scroll.x - event.clientX + pan.current.pointer.x, pan.current.scroll.y - event.clientY + pan.current.pointer.y);
      }}
      onPointerUp={() => { pan.current = null; }} onPointerCancel={() => { pan.current = null; }}>
      {!nodes.length && <div className="empty-state"><h2>No sessions reported yet</h2><p>Native parent relationships will appear here as work starts.</p></div>}
      <div className="flow-space" style={{ width: width * scale, height: height * scale }}>
        <div className="flow-world" style={{ width, height, transform: `scale(${scale})` }}>
          <svg className="flow-connections" width={width} height={height} aria-hidden="true">
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
              const mid = (sy + ey) / 2;
              const path = `M${sx},${sy} C${sx},${mid} ${ex},${mid} ${ex},${ey}`;
              const communication = byID.get(node.parent)?.session?.role === "cfo" && ownsTaskSession(node.session, node.task)
                ? recentCommunication(snapshot.actions, node.task, now, connected) : undefined;
              return <g key={node.id} className={"connection-line " + (communication ? "communicating" : "") + " relation-" + node.relation}>
                <path d={path} /><circle cx={sx} cy={sy} r={4} /><circle cx={ex} cy={ey} r={4} />
                {communication && <><path className="communication-pulse" d={path} /><text x={sx + 12} y={sy + 38}>Guidance accepted</text></>}
              </g>;
            })}
          </svg>
          {visible.map((node) => {
            const p = point(node.id), owner = ownsTaskSession(node.session, node.task);
            const phase = owner ? node.task?.phase : node.session?.runtime?.state || node.session?.phase;
            const children = nodes.some((child) => child.parent === node.id);
            const parent = node.parent ? byID.get(node.parent) : undefined;
            return <article key={node.id} className={"flow-node" + (selected === node.id ? " selected" : "")} style={{ left: p.x, top: p.y, width: NODE_WIDTH, height: NODE_HEIGHT }}>
              <button className="flow-node-main" onPointerDown={(event) => startDrag(event, node)} onPointerMove={moveDrag} onPointerUp={endDrag} onPointerCancel={endDrag}
                onKeyDown={(event) => {
                  if (!event.altKey || !event.key.startsWith("Arrow")) return;
                  event.preventDefault();
                  const step = event.shiftKey ? 50 : 10;
                  move(node.id, { x: p.x + (event.key === "ArrowRight" ? step : event.key === "ArrowLeft" ? -step : 0), y: p.y + (event.key === "ArrowDown" ? step : event.key === "ArrowUp" ? -step : 0) });
                }} onKeyUp={(event) => { if (event.key.startsWith("Arrow")) save(); }}
                onClick={(event) => { if (ignoreClick.current) { ignoreClick.current = false; return; } onSelect(node, event.currentTarget); }} aria-pressed={selected === node.id}
                aria-label={node.title + ". " + nodeStatus(node) + ". " + (parent ? "Parent: " + parent.title : node.relation)} aria-describedby="canvas-help">
                <Avatar persona={personaFor(node.task, node.session)} />
                <span className="card-copy"><strong>{node.title}</strong>{node.task?.project && <span className="project-label">{node.task.project}</span>}<span className={"plain-status phase-" + phase}><span className="status-dot" />{nodeStatus(node)}</span>
                  {!node.parent && node.session?.role !== "cfo" && <small>{node.relation}</small>}
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
      }}>⠿ <span>Arrange</span></button>
      <div><button aria-label="Zoom out" onClick={() => setScale(Math.max(.35, scale - .1))}>−</button><output aria-label="Zoom">{Math.round(scale * 100)}%</output><button aria-label="Zoom in" onClick={() => setScale(Math.min(1.5, scale + .1))}>+</button><button aria-label="Fit canvas" onClick={fit}>⛶</button></div>
    </div>
    {layout.error && <p className="layout-notice" role="status">{layout.error}</p>}
  </section>;
}
