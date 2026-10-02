// The room a tip leaves between itself and its part or card, and at the
// screen's edges.
const GAP = 8;
// What no tip covers: the memory meter, and the controls and links of the task
// cards.
const KEPT_CLEAR = ".memory, .task-card-shell a, .task-card-shell button:not(.task-card)";

// A tip in the way everywhere at its full width is tried again no wider than
// its part, when the part is at least this wide.
const NARROWED_AT_LEAST = 200;

// Puts a tip beside its part: over or under the part, or for a part of a task
// card over or under the whole card, so it never lies on the card. A card's
// tip opens on the side of the card its part is nearer; any other tip opens
// under its part unless its place asks for over with --tip-side. Along its
// part it sits where data-tip-align says, start, end or centered, and it is
// kept inside the screen on every side.
//
// It keeps clear of what KEPT_CLEAR names. On each side it may slide along
// its card past what is in its way, still touching the card; it takes the
// other side when that is clear and the first is not; and over the first card
// of Tasks, which sits under the memory meter, it may open past the meter.
// When no such place is clear it is tried again no wider than its part, which
// keeps a title's tip over the text of the cards around it and off the
// controls beside that text. A tip too tall to be clear anywhere takes the
// place where it covers least.
function place(node: HTMLElement, part: HTMLElement) {
  const shell = part.closest<HTMLElement>(".task-card-shell") ?? part;
  const box = shell.getBoundingClientRect(), anchor = part.getBoundingClientRect();
  const { clientWidth: width, clientHeight: height } = document.documentElement;
  const clear = [...document.querySelectorAll(KEPT_CLEAR)].filter((other) => !shell.contains(other));
  const kept = clear.map((other) => other.getBoundingClientRect());
  const meters = clear.filter((other) => other.matches(".memory")).map((other) => other.getBoundingClientRect());
  const align = part.getAttribute("data-tip-align");
  const isUnderFirst = shell === part ? getComputedStyle(part).getPropertyValue("--tip-side").trim() !== "over" : anchor.top + anchor.height / 2 > box.top + box.height / 2;
  // The best place for the tip at the width it has now, and how much of what
  // it keeps clear of it covers there, in square pixels; a pixel of overlap
  // either way does not count.
  const best = () => {
    const own = node.getBoundingClientRect();
    const asked = align === "start" ? anchor.left : align === "end" ? anchor.right - own.width : anchor.left + anchor.width / 2 - own.width / 2;
    const aligned = Math.max(GAP, Math.min(asked, width - GAP - own.width));
    const under = box.bottom + GAP, over = box.top - GAP - own.height;
    const fits = (top: number) => top >= GAP && top + own.height <= height - GAP;
    const overlap = (other: DOMRect, left: number, top: number) => Math.max(0, Math.min(left + own.width, other.right) - Math.max(left, other.left) - 1) * Math.max(0, Math.min(top + own.height, other.bottom) - Math.max(top, other.top) - 1);
    const covered = (left: number, top: number) => kept.reduce((sum, other) => sum + overlap(other, left, top), 0);
    // On one side of the card: where the tip was asked to sit, then just left
    // or right of everything in its way at that height, still over the card.
    const beside = (top: number) => {
      const row = kept.filter((other) => other.bottom - top > 1 && top + own.height - other.top > 1);
      const slid = [Math.min(...row.map((other) => other.left)) - own.width, Math.max(...row.map((other) => other.right))]
        .filter((left) => left >= GAP && left + own.width <= width - GAP && left < box.right && left + own.width > box.left);
      return [aligned, ...slid].map((left) => ({ left, top }));
    };
    const pastMeter = meters.filter((meter) => overlap(meter, aligned, over) > 0).map((meter) => ({ left: aligned, top: meter.top - GAP - own.height }));
    const places = [...(isUnderFirst ? [under, over] : [over, under]).filter(fits).flatMap(beside), ...pastMeter.filter((spot) => fits(spot.top))];
    // The first place that covers nothing, or failing that the one that covers
    // least; with no room on the screen at all, under, held on the screen.
    let spot = places[0] ?? { left: aligned, top: Math.max(GAP, Math.min(under, height - GAP - own.height)) };
    for (const other of places) if (covered(other.left, other.top) < covered(spot.left, spot.top)) spot = other;
    return { ...spot, covered: covered(spot.left, spot.top), width: own.width };
  };
  node.style.maxWidth = "";
  let spot = best();
  if (spot.covered > 0 && anchor.width >= NARROWED_AT_LEAST && anchor.width < spot.width) {
    node.style.maxWidth = anchor.width + "px";
    const narrowed = best();
    if (narrowed.covered < spot.covered) spot = narrowed;
    else node.style.maxWidth = "";
  }
  node.style.left = spot.left + "px";
  node.style.top = spot.top + "px";
}

// Shows the board's tips until the function it returns is called. Every part
// that carries data-tip shows that text in one tip floating over the whole
// page while the pointer or the keyboard's focus is on the part, so no
// scrolling box clips a tip and every tip can be kept on the screen (see
// place). The tip says what its part's data-tip says now, follows its part as
// the page scrolls, resizes or changes, and goes as soon as the part is left,
// has no tip or has left the page. A task card gains its tip, its shortened
// title in full, only once it is pointed at, so it counts as a part from the
// start. A part inside a modal dialog shows its tip inside the dialog, which
// is drawn over the rest of the page. A card being dragged shows no tip.
export function watchTips(): () => void {
  let part: HTMLElement | null = null, node: HTMLElement | null = null;
  const show = () => {
    const text = part?.isConnected && !part.closest(".sorting") ? part.getAttribute("data-tip") : null;
    if (!part || !text) {
      node?.remove();
      node = null;
      return;
    }
    if (!node) {
      node = document.createElement("div");
      node.className = "tip";
      node.setAttribute("role", "tooltip");
    }
    const host = part.closest("dialog") ?? document.body;
    if (node.parentElement !== host) host.append(node);
    if (node.textContent !== text) node.textContent = text;
    place(node, part);
  };
  // Watches the page only while a part is pointed at.
  const changes = new MutationObserver(show);
  const point = (target: EventTarget | null) => {
    const next = target instanceof Element ? target.closest<HTMLElement>("[data-tip], .task-card") : null;
    if (next === part) return;
    part = next;
    changes.disconnect();
    if (part) changes.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["data-tip"] });
    show();
  };
  const onOver = (event: PointerEvent) => point(event.target);
  // The pointer left the window, or a finger lifted.
  const onOut = (event: PointerEvent) => { if (!event.relatedTarget) point(null); };
  const onFocus = (event: FocusEvent) => { if (event.target instanceof Element && event.target.matches(":focus-visible")) point(event.target); };
  const onBlur = () => point(null);
  document.addEventListener("pointerover", onOver);
  document.addEventListener("pointerout", onOut);
  document.addEventListener("focusin", onFocus);
  document.addEventListener("focusout", onBlur);
  addEventListener("scroll", show, { capture: true, passive: true });
  addEventListener("resize", show);
  return () => {
    point(null);
    document.removeEventListener("pointerover", onOver);
    document.removeEventListener("pointerout", onOut);
    document.removeEventListener("focusin", onFocus);
    document.removeEventListener("focusout", onBlur);
    removeEventListener("scroll", show, { capture: true });
    removeEventListener("resize", show);
  };
}
