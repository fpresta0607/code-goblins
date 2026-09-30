// Watches a page's transient effects frame by frame, the way a viewer sees
// them: an element that leaves the page while still visible disappeared at
// once, and an element that stopped animating while still visible and later
// left sat stale before it went.
export interface EffectReport {
  // removed counts the elements that left the page and ended those of them
  // that had animated; peak is the most visible any of them ever was, and
  // animated counts the targets that animated.
  removed: number;
  ended: number;
  peak: number;
  animated: number;
  leftVisible: string[];
  stale: string[];
  running: number;
  elements: number;
}

export interface EffectWatch { report: () => EffectReport; stop: () => void }

declare global {
  interface Window { effectWatch?: EffectWatch }
}

function describe(element: Element, pseudo = ""): string {
  const classes = (element.getAttribute("class") || "").trim().split(/\s+/).filter(Boolean);
  return element.tagName.toLowerCase() + classes.map((name) => "." + name).join("") + pseudo;
}

// shown is how visible an element is on screen: its own opacity times every
// ancestor's, and nothing when any of them is not displayed.
function shown(element: Element, pseudo = ""): number {
  let value = 1;
  for (let at: Element | null = element; at && at !== document.documentElement; at = at.parentElement) {
    const style = getComputedStyle(at, at === element && pseudo ? pseudo : null);
    if (style.display === "none" || style.visibility === "hidden") return 0;
    value *= Number(style.opacity);
  }
  return value;
}

export function watchEffects(root: Element): EffectWatch {
  const opacity = new WeakMap<Element, number>();
  const peak = new WeakMap<Element, number>();
  const staleFrames = new Map<Element, Map<string, number>>();
  const report: EffectReport = { removed: 0, ended: 0, peak: 0, animated: 0, leftVisible: [], stale: [], running: 0, elements: 0 };
  let frame = 0;
  const sample = () => {
    for (const element of root.querySelectorAll("*")) {
      const value = shown(element);
      opacity.set(element, value);
      peak.set(element, Math.max(peak.get(element) || 0, value));
    }
    const running = new Map<Element, Set<string>>();
    for (const animation of document.getAnimations()) {
      if (!(animation.effect instanceof KeyframeEffect) || !animation.effect.target || !root.contains(animation.effect.target)) continue;
      const { target, pseudoElement } = animation.effect;
      const pseudo = pseudoElement || "";
      if (!staleFrames.get(target)?.has(pseudo)) {
        staleFrames.set(target, (staleFrames.get(target) || new Map()).set(pseudo, 0));
        report.animated++;
      }
      if (animation.playState === "running") running.set(target, (running.get(target) || new Set()).add(pseudo));
    }
    for (const [target, pseudos] of staleFrames) {
      if (!target.isConnected) continue;
      for (const [pseudo, count] of pseudos) {
        if (!running.get(target)?.has(pseudo) && shown(target, pseudo) > .02) pseudos.set(pseudo, count + 1);
      }
    }
    frame = requestAnimationFrame(sample);
  };
  const observer = new MutationObserver((records) => {
    for (const record of records) for (const node of record.removedNodes) {
      if (!(node instanceof Element)) continue;
      for (const element of [node, ...node.querySelectorAll("*")]) {
        report.removed++;
        report.peak = Math.max(report.peak, peak.get(element) || 0);
        if ((opacity.get(element) || 0) > .02) report.leftVisible.push(describe(element));
        if (staleFrames.has(element)) report.ended++;
        for (const [pseudo, count] of staleFrames.get(element) || []) if (count > 0) report.stale.push(describe(element, pseudo) + " for " + count + " frames");
      }
    }
  });
  observer.observe(root, { childList: true, subtree: true });
  sample();
  return {
    report: () => ({ ...report, running: document.getAnimations().filter((animation) => animation.playState === "running").length, elements: root.querySelectorAll("*").length }),
    stop: () => { observer.disconnect(); cancelAnimationFrame(frame); },
  };
}
