import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { FleetTree } from "./types";
import { treeTraffic, type Departure, type TreeSeen, type TreeTraffic } from "./tree-traffic";

// How long a baby goblin's birth and its exit take, how long a pulse runs
// along its branch, and how long a baby goblin slides to a new place when one
// beside it comes or goes. An exit lasts a pulse and a fade, so a baby goblin
// that finished reports back to its goblin and then goes.
export const BIRTH_MS = 520, BRANCH_PULSE_MS = 900, EXIT_MS = 1400, GLIDE_MS = 380;

// A pulse on a baby goblin's branch: down from its goblin when the goblin
// starts it, up to its goblin when it reports or finishes.
export interface BranchPulse { key: string; goblin: string; child: string; isUp: boolean }

export interface Point { x: number; y: number }

export const isStill = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

// useTreeMotion plays what happens on the goblins' branches as it happens: a
// baby goblin born grows in, one that leaves fades out where it hung, and a
// pulse runs along a branch at each exchange. Each plays once, for the
// snapshot that showed it, and is gone when it ends: nothing runs between
// snapshots, nothing plays while the board is hidden or its stream is down,
// and with reduced motion a baby goblin simply appears or goes.
export function useTreeMotion(trees: Record<string, FleetTree>, isLive: boolean) {
  // What a snapshot sets moving is worked out while it renders, so its first
  // frame already shows a baby goblin born, leaving or pulsing.
  const [state, setState] = useState<{ trees: Record<string, FleetTree> | null; seen: TreeSeen | null; motion: Motion[]; batch: number }>({ trees: null, seen: null, motion: [], batch: 0 });
  let { motion } = state;
  if (state.trees !== trees) {
    const traffic = treeTraffic(state.seen, trees), batch = state.batch + 1;
    if (isLive && !document.hidden && !isStill()) motion = [...motion, ...moving(traffic, batch)];
    // A baby goblin's branch carries one pulse at a time, its newest.
    motion = motion.filter((one, i) => one.kind !== "pulse" || !motion.slice(i + 1).some((later) => later.kind === "pulse" && later.pulse.child === one.pulse.child));
    setState({ trees, seen: traffic.seen, motion, batch });
  }
  // Each motion ends its own time after the frame that started it, on a
  // timer set for that alone, and none is set while nothing moves.
  const timed = useRef(new Map<string, ReturnType<typeof setTimeout>>());
  useEffect(() => {
    for (const one of state.motion) {
      if (timed.current.has(one.key)) continue;
      timed.current.set(one.key, setTimeout(() => {
        timed.current.delete(one.key);
        setState((prior) => ({ ...prior, motion: prior.motion.filter((other) => other.key !== one.key) }));
      }, one.ms));
    }
  }, [state.motion]);
  useEffect(() => {
    const pending = timed.current;
    return () => pending.forEach(clearTimeout);
  }, []);
  return {
    born: new Set(motion.flatMap((one) => one.kind === "born" ? [one.child] : [])),
    leaving: motion.flatMap((one) => one.kind === "leaving" ? [one.departure] : []),
    pulses: motion.flatMap((one) => one.kind === "pulse" ? [one.pulse] : []),
    // A baby goblin came or went, and the canvas still eases to its new fit.
    isSettling: motion.some((one) => one.kind === "settling"),
  };
}

// A motion on the branches, by a key of its own, and how long it lasts.
// Settling lasts until the frame has eased to the fit a birth or an exit left.
type Motion = { key: string; ms: number } & ({ kind: "born"; child: string } | { kind: "leaving"; departure: Departure } | { kind: "pulse"; pulse: BranchPulse } | { kind: "settling" });

// moving is what a snapshot's traffic sets moving: each baby goblin born
// grows in with a pulse down its branch, each that left reports up its branch
// and fades, and each that reported sends a pulse up its branch. batch counts
// the snapshots, so each motion's key is its own.
function moving(traffic: TreeTraffic, batch: number): Motion[] {
  const pulse = (goblin: string, child: string, isUp: boolean): Motion => {
    const key = (isUp ? "up:" : "down:") + child + ":" + batch;
    return { kind: "pulse", key, ms: BRANCH_PULSE_MS, pulse: { key, goblin, child, isUp } };
  };
  const settling: Motion[] = traffic.left.length ? [{ kind: "settling", key: "settling:" + batch, ms: EXIT_MS + GLIDE_MS }]
    : traffic.born.length ? [{ kind: "settling", key: "settling:" + batch, ms: GLIDE_MS }] : [];
  return [
    ...settling,
    ...traffic.born.flatMap((one): Motion[] => [{ kind: "born", key: "born:" + one.child + ":" + batch, ms: BIRTH_MS, child: one.child }, pulse(one.goblin, one.child, false)]),
    ...traffic.left.flatMap((gone): Motion[] => [{ kind: "leaving", key: "leaving:" + gone.node.id + ":" + batch, ms: EXIT_MS, departure: gone }, pulse(gone.goblin, gone.node.id, true)]),
    ...traffic.reported.map((one) => pulse(one.goblin, one.child, true)),
  ];
}

// useGlide slides each element it is given to its new place, when one beside
// it comes or goes, instead of letting it jump there: a transform from where
// it was to nothing, played once. places are where each element is drawn,
// by its key, in the frame they share.
export function useGlide(places: Map<string, Point>, isLive: boolean) {
  const elements = useRef(new Map<string, HTMLElement>());
  const before = useRef(new Map<string, Point>());
  useLayoutEffect(() => {
    const isMoving = isLive && !document.hidden && !isStill();
    for (const [key, place] of places) {
      const was = before.current.get(key), element = elements.current.get(key);
      if (isMoving && was && element && (was.x !== place.x || was.y !== place.y)) {
        element.animate([{ transform: `translate(${was.x - place.x}px, ${was.y - place.y}px)` }, { transform: "none" }], { duration: GLIDE_MS, easing: "cubic-bezier(.2, .8, .2, 1)" });
      }
    }
    before.current = places;
  });
  return (key: string) => (element: HTMLElement | null) => {
    if (element) elements.current.set(key, element);
    else elements.current.delete(key);
  };
}

// travel plays a pulse along a branch's points, from its first to its last,
// fading in as it leaves and out as it arrives: transform and opacity only,
// which the browser plays off the main thread. The pulse is unseen before and
// after, so nothing lingers once it ends.
export function travel(points: Point[]) {
  return (element: HTMLElement | null) => {
    if (!element || element.getAnimations().length || points.length < 2) return;
    const lengths = points.reduce((sums, point, i) => [...sums, i ? sums[i - 1] + Math.abs(point.x - points[i - 1].x) + Math.abs(point.y - points[i - 1].y) : 0], [] as number[]);
    const total = lengths[lengths.length - 1] || 1;
    const timing = { duration: BRANCH_PULSE_MS, easing: "ease-in-out" };
    element.animate(points.map((point, i) => ({ transform: `translate(${point.x}px, ${point.y}px)`, offset: lengths[i] / total })), timing);
    element.animate([{ opacity: 0 }, { opacity: 1, offset: .15 }, { opacity: 1, offset: .85 }, { opacity: 0 }], timing);
  };
}
