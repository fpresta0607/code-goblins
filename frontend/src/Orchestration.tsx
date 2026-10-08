import { useEffect, useMemo, useRef, useState, type PointerEvent } from "react";
import type { BoardActivity, FleetTree, Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { activityDisplay, EFFECT_MS, playFrom, presentationShownOn, type ActivityEffect } from "./activity";
import { Chevron } from "./Chevron";
import { Icon } from "./Icon";
import { ownsTaskSession } from "./lineageTree";
import { arrange, asksOverlord, expireTraffic, fitScale, fleetTraffic, makeRoom, NODE_HEIGHT, NODE_WIDTH, nodeStatus, personaFor, PULSE_MS, reportTraffic, settle, statusPhase, waitingOn, workflowNodes, zoomAt, type Extent, type Point, type View, type WorkflowNode } from "./workflow";
import { branchLayout, hasRunningChildren, running } from "./fleet-tree";
import { TreeCount } from "./TreeCount";
import { TreeChild } from "./TreeChild";

// v1 saved every card's place at each drag, which pinned the whole canvas.
const layoutKey = "cfo-orchestration-layout-v2";

// Under a folded goblin's card: the gap to its count, and the count's height.
const TREE_GAP = 18, COUNT_HEIGHT = 40;

// How far a notch of the wheel zooms: about a sixth.
const WHEEL_ZOOM = .0015;

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

export function Orchestration({ snapshot, selected, connected, effects, onSelect, presentations, now }: {
  presentations:BoardActivity[];
  snapshot: Snapshot; selected: string; connected: boolean; effects: ActivityEffect[]; now: number;
  onSelect: (node: WorkflowNode, source: HTMLElement) => void;
}) {
  const nodes = useMemo(() => workflowNodes(snapshot), [snapshot]);
  const awaited = useMemo(() => waitingOn(snapshot, nodes), [snapshot, nodes]);
  // A goblin's running children hang on branches under its card, and the
  // rows below make room for them; folding the goblin, as its chevron does
  // with its descendants, leaves their count. Finished children are not drawn.
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const trees = useMemo(() => Object.fromEntries(nodes.flatMap((node): [string, FleetTree][] => {
    const tree = node.task && ownsTaskSession(node.session, node.task) ? node.task.tree : undefined;
    return tree && hasRunningChildren(tree) ? [[node.id, tree]] : [];
  })), [nodes]);
  const branches = useMemo(() => Object.fromEntries(Object.entries(trees).map(([id, tree]) => [id, branchLayout(running(tree).length, NODE_WIDTH)])), [trees]);
  const extents = useMemo(() => Object.fromEntries(Object.keys(trees).map((id): [string, Extent] => collapsed.has(id)
    ? [id, { width: NODE_WIDTH, below: TREE_GAP + COUNT_HEIGHT }] : [id, { width: branches[id].width, below: branches[id].height }])), [trees, branches, collapsed]);
  const below = useMemo(() => Object.fromEntries(Object.entries(extents).map(([id, extent]) => [id, extent.below])), [extents]);
  // The graph fills the visible canvas, centered, and Arrange lays it out in
  // the rows that show it largest there, until a zoom or a pan by hand takes
  // over; Fit hands it back. A dragged card holds the frame still.
  const [canvas, setCanvas] = useState({ width: 0, height: 0 });
  const automatic = useMemo(() => makeRoom(arrange(nodes, canvas, awaited, extents), below), [nodes, canvas, awaited, extents, below]);
  const [layout, setLayout] = useState(readLayout);
  const positions = useMemo(() => settle(automatic, layout.positions, extents), [automatic, layout.positions, extents]);
  const [view, setView] = useState<View | null>(null);
  const [held, setHeld] = useState<View | null>(null);

  const viewport = useRef<HTMLDivElement>(null);
  const ignoreClick = useRef(false);
  const drag = useRef<{ id: string; pointer: Point; start: Point } | null>(null);
  const pan = useRef<{ pointer: Point; start: View } | null>(null);
  const signatures = useRef<Map<string, string> | null>(null);
  const [traffic, setTraffic] = useState<Record<string, number[]>>({});
  const pulses = useRef(new Set<ReturnType<typeof setTimeout>>());
  useEffect(() => {
    const { signatures: next, moved } = fleetTraffic(signatures.current, snapshot);
    signatures.current = next;
    // A report that lands while the board is hidden was never seen, so it
    // never plays later.
    if (!moved.length || document.hidden) return;
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
  const point = (id: string) => positions[id];
  // Each card's left and right with what hangs under it, centred on the card.
  const lefts = visible.map((node) => point(node.id).x + (NODE_WIDTH - (extents[node.id]?.width || NODE_WIDTH)) / 2);
  const rights = visible.map((node) => point(node.id).x + (NODE_WIDTH + (extents[node.id]?.width || NODE_WIDTH)) / 2);
  const ys = visible.map((node) => point(node.id).y);
  const fitLeft = lefts.length ? Math.min(...lefts) - 40 : 0;
  const fitTop = ys.length ? Math.min(...ys) - 40 : 0;
  const fitWidth = Math.max(1, ...rights.map((right) => right + 40)) - fitLeft;
  const fitHeight = Math.max(1, ...visible.map((node) => point(node.id).y + NODE_HEIGHT + (below[node.id] || 0) + 80)) - fitTop;
  const fitted = fitScale({ width: fitWidth, height: fitHeight }, canvas);
  const shown = held ?? view ?? { scale: fitted, x: (canvas.width - fitWidth * fitted) / 2 - fitLeft * fitted, y: (canvas.height - fitHeight * fitted) / 2 - fitTop * fitted };
  const { scale } = shown;
  const zoom = (next: number) => setView(zoomAt(shown, next, { x: canvas.width / 2, y: canvas.height / 2 }));
  const fit = () => setView(null);
  useEffect(() => {
    const element = viewport.current;
    if (!element) return;
    const observer = new ResizeObserver(() => setCanvas({ width: element.clientWidth, height: element.clientHeight }));
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  // The wheel zooms where the pointer is, as a map does; it is listened to
  // here because React listens to the wheel passively, which cannot stop the
  // page scrolling.
  const showing = useRef(shown);
  useEffect(() => { showing.current = shown; });
  useEffect(() => {
    const element = viewport.current;
    if (!element) return;
    const wheel = (event: WheelEvent) => {
      event.preventDefault();
      const box = element.getBoundingClientRect(), lines = event.deltaMode === WheelEvent.DOM_DELTA_LINE ? 40 : 1;
      setView(zoomAt(showing.current, showing.current.scale * Math.exp(-event.deltaY * lines * WHEEL_ZOOM), { x: event.clientX - box.left, y: event.clientY - box.top }));
    };
    element.addEventListener("wheel", wheel, { passive: false });
    return () => element.removeEventListener("wheel", wheel);
  }, []);
  const move = (id: string, next: Point) => setLayout((prior) => ({ ...prior, positions: {
    ...prior.positions, [id]: { x: Math.max(0, Math.min(50000, next.x)), y: Math.max(0, Math.min(50000, next.y)) },
  } }));
  // Only the cards he placed by hand are saved, so the canvas keeps arranging
  // every other card around them.
  const save = () => {
    try {
      const retained = Object.fromEntries(nodes.flatMap((node) => layout.positions[node.id] ? [[node.id, layout.positions[node.id]]] : []));
      localStorage.setItem(layoutKey, JSON.stringify(retained));
    } catch { setLayout((prior) => ({ ...prior, error: "Layout could not be saved in this browser." })); }
  };
  const startDrag = (event: PointerEvent<HTMLButtonElement>, node: WorkflowNode) => {
    if (event.button !== 0) return;
    event.stopPropagation();
    event.currentTarget.setPointerCapture(event.pointerId);
    drag.current = { id: node.id, pointer: { x: event.clientX, y: event.clientY }, start: point(node.id) };
    setHeld(shown);
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
  const toggle = (id: string) => setCollapsed((prior) => { const next = new Set(prior); if (next.has(id)) next.delete(id); else next.add(id); return next; });
  return <section className="orchestration" aria-label="Orchestration">
    <p className="sr-only" id="canvas-help">Drag a card to reposition it. With a card focused, Alt and arrow keys move it, Shift moves farther. Drag the canvas to move the view, and the wheel zooms where the pointer is. On the canvas, plus and minus zoom, zero fits. Moving cards never changes parent relationships.</p>
    <div ref={viewport} className="flow-canvas" tabIndex={0} aria-label="Connected family tree" aria-describedby="canvas-help"
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key === "+" || event.key === "=") { event.preventDefault(); zoom(scale + .1); }
        if (event.key === "-") { event.preventDefault(); zoom(scale - .1); }
        if (event.key === "0") { event.preventDefault(); fit(); }
      }}
      onPointerDown={(event) => {
        if (event.button !== 0 || (event.target instanceof Element && event.target.closest(".flow-node, button"))) return;
        event.currentTarget.setPointerCapture(event.pointerId);
        pan.current = { pointer: { x: event.clientX, y: event.clientY }, start: shown };
      }}
      onPointerMove={(event) => {
        if (!pan.current) return;
        const { pointer, start } = pan.current, dx = event.clientX - pointer.x, dy = event.clientY - pointer.y;
        // A press that hardly moves is not a pan, so it leaves the frame fitted.
        if (!view && Math.abs(dx) + Math.abs(dy) < 4) return;
        setView({ ...start, x: start.x + dx, y: start.y + dy });
      }}
      onPointerUp={() => { pan.current = null; }} onPointerCancel={() => { pan.current = null; }}>
      {!nodes.length && <div className="empty-state"><h2>No sessions reported yet</h2><p>Native parent relationships will appear here as work starts.</p></div>}
      <div className="flow-world" style={{ transform: `translate(${shown.x}px, ${shown.y}px) scale(${scale})` }}>
        <svg className="flow-connections" width={fitLeft + fitWidth} height={fitTop + fitHeight} aria-hidden="true">
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
            const reports = connected && node.task ? traffic[node.task.id] || [] : [];
            // Each pulse or birth highlight is its own overlay on the
            // connector, played out over its whole life and faded before
            // it is removed, so the connector itself never changes.
            const effect = (key: string, kind: "communicating" | "creating", start: number, life: number) =>
              <g key={key} ref={playFrom(start)} className={"connector-effect " + kind} style={{ animationDuration: life + "ms" }}>
                <path d={path} /><circle cx={sx} cy={sy} r={4} /><circle cx={ex} cy={ey} r={4} />
                {kind === "communicating" && <path className="communication-pulse" d={path} />}
              </g>;
            return <g key={node.id} className={"connection-line relation-" + node.relation}>
              <path d={path} /><circle cx={sx} cy={sy} r={4} /><circle cx={ex} cy={ey} r={4} />
              {activity.creation && effect(activity.creation.id, "creating", activity.creation.expires - EFFECT_MS, EFFECT_MS)}
              {activity.communication && effect(activity.communication.id, "communicating", activity.communication.expires - EFFECT_MS, EFFECT_MS)}
              {reports.map((report) => effect("report:" + report, "communicating", report, PULSE_MS))}
            </g>;
          })}
          {/* A goblin waiting on another goblin: a dashed line from the
              waiting card to the one it waits on, apart from the family tree. */}
          {visible.flatMap((node) => {
            const target = visible.find((other) => other.id === awaited[node.id]);
            if (!target) return [];
            const from = point(node.id), to = point(target.id);
            let path: string, ex: number, ey: number;
            if (Math.abs(to.y - from.y) < NODE_HEIGHT) {
              // Same row: an arc under both cards, clear of the tree above.
              const sx = from.x + NODE_WIDTH / 2, sy = from.y + NODE_HEIGHT, dip = Math.max(from.y, to.y) + NODE_HEIGHT + 56;
              ex = to.x + NODE_WIDTH / 2; ey = to.y + NODE_HEIGHT;
              path = `M${sx},${sy} C${sx},${dip} ${ex},${dip} ${ex},${ey}`;
            } else if (Math.abs(to.x - from.x) < NODE_WIDTH) {
              // One under the other, as the canvas arranges a waiting
              // goblin: straight down the middle of what they share, apart
              // from the waiting card's own connector at its center.
              ex = (Math.max(from.x, to.x) + Math.min(from.x, to.x) + NODE_WIDTH) / 2;
              const upper = to.y < from.y;
              const sy = upper ? from.y : from.y + NODE_HEIGHT;
              ey = upper ? to.y + NODE_HEIGHT : to.y;
              path = `M${ex},${sy} L${ex},${ey}`;
            } else {
              const right = to.x > from.x;
              const sx = from.x + (right ? NODE_WIDTH : 0), sy = from.y + NODE_HEIGHT / 2;
              ex = to.x + (right ? 0 : NODE_WIDTH); ey = to.y + NODE_HEIGHT / 2;
              const bend = Math.max(60, Math.abs(ex - sx) / 2) * (right ? 1 : -1);
              path = `M${sx},${sy} C${sx + bend},${sy} ${ex - bend},${ey} ${ex},${ey}`;
            }
            return [<g key={"wait:" + node.id} className="dependency-line"><path d={path} /><circle cx={ex} cy={ey} r={5} /></g>];
          })}
        </svg>
        {visible.map((node) => {
          const parent = node.parent ? byID.get(node.parent) : undefined;
          const activity = activityDisplay(connected ? effects : [],node.session?.id || "",parent?.session?.id || "");
          const effect = activity.received || activity.created;
          const p = point(node.id), owner = ownsTaskSession(node.session, node.task);
          const phase = owner && node.task ? statusPhase(node.task) : node.session?.runtime?.state || node.session?.phase;
          const children = nodes.some((child) => child.parent === node.id) || !!trees[node.id];
          const asking = owner && asksOverlord(snapshot, node.task?.id || ""), status = nodeStatus(node, asking);
          return <article key={node.id} className={"flow-node" + (activity.created ? " node-enter" : "") + (selected === node.id ? " selected" : "")} style={{ left: p.x, top: p.y, width: NODE_WIDTH, height: NODE_HEIGHT }}>
            {effect && <span key={effect.id} ref={playFrom(effect.expires - EFFECT_MS)} className="activity-glow" aria-hidden="true" />}
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
              <span className="card-copy"><strong>{node.title}</strong>{presentations.some(a=>presentationShownOn(a,node.session,node.task))&&<span className="browser-indicator">Browser active</span>}{node.task?.project && <span className="project-label">{node.task.project}</span>}<span className={"plain-status phase-" + phase}><span className="status-dot" />{status}</span>
                {!node.parent && node.session?.role !== "cfo" && !node.cfo && <small>{node.relation}</small>}
              </span>
            </button>
            {children && <button className="node-disclosure" aria-label={(collapsed.has(node.id) ? "Expand" : "Collapse") + " descendants of " + node.title} aria-expanded={!collapsed.has(node.id)} onClick={() => toggle(node.id)}><Chevron collapsed={collapsed.has(node.id)} /></button>}
          </article>;
        })}
        {visible.flatMap((node) => {
          const tree = trees[node.id];
          if (!tree) return [];
          const p = point(node.id);
          if (collapsed.has(node.id)) return [<div key={"tree:" + node.id} className="tree-branch" style={{ left: p.x, top: p.y + NODE_HEIGHT + TREE_GAP, width: NODE_WIDTH }}>
            <TreeCount tree={tree} title={node.title} expanded={false} onToggle={() => toggle(node.id)} />
          </div>];
          const block = branches[node.id];
          return [<ul key={"tree:" + node.id} className="tree-branches" aria-label={"What runs under " + node.title}
            style={{ left: p.x + (NODE_WIDTH - block.width) / 2, top: p.y + NODE_HEIGHT, width: block.width, height: block.height }}>
            <svg className="branch-lines" width={block.width} height={block.height} aria-hidden="true">
              {block.lines.map((line) => <path key={line} d={line} />)}
              {block.ends.map((end) => <circle key={end.x + "," + end.y} cx={end.x} cy={end.y} r={3} />)}
            </svg>
            {running(tree).map((child, i) => <li key={child.id} style={{ left: block.places[i].x, top: block.places[i].y }}><TreeChild node={child} now={now} /></li>)}
          </ul>];
        })}
      </div>
    </div>
    <div className="canvas-controls">
      <button onClick={() => {
        setLayout({ positions: {}, error: "" }); setCollapsed(new Set()); setView(null);
        try { localStorage.removeItem(layoutKey); }
        catch { setLayout({ positions: {}, error: "Layout could not be cleared in this browser." }); }
      }} className="icon-button" aria-label="Arrange" data-tip="Arrange" data-tip-align="start"><Icon name="arrange" /></button>
      <div><button className="icon-button" aria-label="Zoom out" data-tip="Zoom out" onClick={() => zoom(scale - .1)}><Icon name="minus" /></button><output aria-label="Zoom">{Math.round(scale * 100)}%</output><button className="icon-button" aria-label="Zoom in" data-tip="Zoom in" onClick={() => zoom(scale + .1)}><Icon name="plus" /></button><button className="icon-button" aria-label="Fit canvas" data-tip="Fit" data-tip-align="end" onClick={fit}><Icon name="fit" /></button></div>
    </div>
    {layout.error && <p className="layout-notice" role="status">{layout.error}</p>}
  </section>;
}
