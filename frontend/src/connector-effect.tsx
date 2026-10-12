import { useLayoutEffect, useRef, useState } from "react";
import { playFrom } from "./activity";
import { isStill, type Point } from "./use-tree-motion";

// The CFO's pulse is a dash 14 px long, 230 px from the next, travelling
// 244 px each 2.5 s along its connector, as the dash pattern it was first
// drawn with. A dash turns with its connector through a point every 6 px.
const DASH = 14, DASH_GAP = 230, DASH_LOOP_MS = 2500, DASH_STEP = 6;

// How a pulse's dashes travel one connector: how many travel it at once, each
// a loop of its own behind the one before, through these frames.
interface DashRoute { path: string; count: number; frames: Keyframe[] }

// ConnectorEffect plays on a connector what the CFO's report or a goblin's
// birth shows: the connector lit, and for a report its dashes travelling
// down it. It lies over the canvas's connectors apart from them, so its fade
// is opacity and its dashes move by transform, which the browser plays off
// the main thread, and nothing on the canvas repaints while it plays. It
// plays on the clock of what it shows, from start, for life.
export function ConnectorEffect({ kind, path, end, width, height, isDelegated, start, life }: {
  kind: "communicating" | "creating"; path: string; end: Point; width: number; height: number;
  isDelegated: boolean; start: number; life: number;
}) {
  const line = useRef<SVGPathElement>(null);
  const [route, setRoute] = useState<DashRoute | null>(null);
  // The dashes follow the connector as it is drawn, so the route is measured
  // from the line once it is laid out, and again whenever the line moves.
  useLayoutEffect(() => {
    if (kind === "communicating" && line.current && !isStill()) setRoute(dashRoute(line.current, path));
  }, [kind, path]);
  const dashes = route?.path === path ? route : null;
  return <div ref={playFrom(start)} className={"connector-effect " + kind + (isDelegated ? " delegated" : "")}
    style={{ width, height, animationDuration: life + "ms" }} aria-hidden="true">
    <svg width={width} height={height}><path ref={line} d={path} /><circle cx={end.x} cy={end.y} r={4} /></svg>
    {dashes && Array.from({ length: dashes.count }, (_, i) => <span key={path + ":" + i} className="communication-pulse" ref={(element) => {
      if (element && !element.getAnimations().length) element.animate(dashes.frames, { duration: dashes.count * DASH_LOOP_MS, delay: -i * DASH_LOOP_MS, iterations: Infinity });
    }} />)}
  </div>;
}

// dashRoute is how a dash travels the line: in from under the card the line
// leaves, along it, turning with it, and out under the card it reaches, then
// unseen until its turn comes round again.
function dashRoute(line: SVGPathElement, path: string): DashRoute {
  const length = line.getTotalLength(), along = length + DASH;
  const count = Math.max(1, Math.ceil(along / (DASH + DASH_GAP)));
  // The share of a dash's loop it spends on the line.
  const shown = along / (count * (DASH + DASH_GAP));
  const at = (distance: number) => line.getPointAtLength(Math.min(length, Math.max(0, distance)));
  // Its heading beyond either end is the line's at that end.
  const heading = (distance: number) => {
    const middle = Math.min(length - 1, Math.max(1, distance)), [from, to] = [at(middle - 1), at(middle + 1)];
    return Math.atan2(to.y - from.y, to.x - from.x) * 180 / Math.PI;
  };
  const frames: Keyframe[] = [];
  let turned = 0;
  for (let travelled = 0; travelled <= along; travelled = Math.min(along, travelled + DASH_STEP)) {
    const distance = travelled - DASH / 2, point = at(distance), angle = heading(distance);
    // Beyond either end it carries on straight, out of sight under the card.
    const past = distance < 0 ? distance : Math.max(0, distance - length);
    const [x, y] = [point.x + past * Math.cos(angle * Math.PI / 180), point.y + past * Math.sin(angle * Math.PI / 180)];
    // A heading keeps turning the short way, never back round the circle.
    turned = frames.length ? turned + ((angle - turned) % 360 + 540) % 360 - 180 : angle;
    frames.push({ offset: shown * travelled / along, opacity: 1, transform: `translate(${x}px, ${y}px) rotate(${turned}deg)` });
    if (travelled === along) break;
  }
  const last = frames[frames.length - 1].transform;
  frames.push({ offset: shown, opacity: 0, transform: last }, { offset: 1, opacity: 0, transform: last });
  return { path, count, frames };
}
