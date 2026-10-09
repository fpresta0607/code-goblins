// The room between a tip and its part, which its arrow spans, and at the
// screen's edges.
const GAP = 8;
// How close to a tip's corner its arrow may sit, clear of the rounding.
const ARROW_INSET = 14;

// The sides of its part a tip tries, in order.
const SIDES = ["above", "below", "right", "left"] as const;
type Side = typeof SIDES[number];

// Puts a tip against its part: above it, else below it, else to its right,
// else to its left, on the first side where the whole tip fits on the
// screen. Along that side it is centered on the part and kept on the screen,
// and its arrow points at the part's middle. A tip that fits on no side goes
// below its part, kept on the screen.
function place(node: HTMLElement, part: HTMLElement) {
  const anchor = part.getBoundingClientRect(), own = node.getBoundingClientRect();
  const { clientWidth: width, clientHeight: height } = document.documentElement;
  const centered = (start: number, length: number, size: number, room: number) => Math.max(GAP, Math.min(start + length / 2 - size / 2, room - GAP - size));
  const left = centered(anchor.left, anchor.width, own.width, width), top = centered(anchor.top, anchor.height, own.height, height);
  const spots: Record<Side, { left: number; top: number }> = {
    above: { left, top: anchor.top - GAP - own.height },
    below: { left, top: anchor.bottom + GAP },
    right: { left: anchor.right + GAP, top },
    left: { left: anchor.left - GAP - own.width, top },
  };
  const fits = ({ left, top }: { left: number; top: number }) => left >= GAP && top >= GAP && left + own.width <= width - GAP && top + own.height <= height - GAP;
  const side = SIDES.find((at) => fits(spots[at])) ?? "below";
  const spot = { left: spots[side].left, top: Math.max(GAP, Math.min(spots[side].top, height - GAP - own.height)) };
  const isAcross = side === "above" || side === "below";
  const arrow = isAcross ? anchor.left + anchor.width / 2 - spot.left : anchor.top + anchor.height / 2 - spot.top;
  if (node.dataset.side !== side) node.dataset.side = side;
  node.style.setProperty("--arrow", Math.max(ARROW_INSET, Math.min(arrow, (isAcross ? own.width : own.height) - ARROW_INSET)) + "px");
  node.style.left = spot.left + "px";
  node.style.top = spot.top + "px";
}

// Shows the board's tips until the function it returns is called. Every part
// that carries data-tip shows that text in one tip floating over the whole
// page, on the frame the pointer or the keyboard's focus comes to the part,
// for as long as either stays, so no scrolling box clips a tip and every tip
// can be kept on the screen beside its part (see place). While a part is
// pointed at its tip is drawn again on every frame, so it says what its
// part's data-tip says now and follows its part as the page scrolls, resizes
// or changes and as the part itself moves, as a card lifting under the
// pointer does. It goes on the frame the part is left, has no tip or has left
// the page, and at any press. A task card gains its tip, its shortened title
// in full, only once it is pointed at, so it counts as a part from the start.
// A part inside a modal dialog shows its tip inside the dialog, which is
// drawn over the rest of the page. A card being dragged shows no tip.
export function watchTips(): () => void {
  let part: HTMLElement | null = null, node: HTMLElement | null = null, frame = 0;
  const show = () => {
    cancelAnimationFrame(frame);
    if (!part?.isConnected) part = null;
    if (part) frame = requestAnimationFrame(show);
    const text = part && !part.closest(".sorting") ? part.getAttribute("data-tip") : null;
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
  const partOf = (target: EventTarget | null) => target instanceof Element ? target.closest<HTMLElement>("[data-tip], .task-card") : null;
  const point = (next: HTMLElement | null) => {
    if (next === part) return;
    part = next;
    show();
  };
  // A pressed mouse or pen shows no tip until it is released, so a card has
  // none from the press that starts its drag; a finger held down shows one
  // for as long as it is down.
  const onPointer = (event: PointerEvent) => point(event.buttons && event.pointerType !== "touch" ? null : partOf(event.target));
  // The pointer left the window, or a finger lifted.
  const onOut = (event: PointerEvent) => { if (!event.relatedTarget) point(null); };
  const onFocus = (event: FocusEvent) => { if (event.target instanceof Element && event.target.matches(":focus-visible")) point(partOf(event.target)); };
  const onBlur = () => point(null);
  // The pointer is listened to as its events go down to their target, so a
  // part that keeps a press to itself, as a canvas node starting its drag
  // does, still takes its tip away.
  const capture = { capture: true };
  document.addEventListener("pointerover", onPointer, capture);
  document.addEventListener("pointerdown", onPointer, capture);
  document.addEventListener("pointerup", onPointer, capture);
  document.addEventListener("pointerout", onOut, capture);
  document.addEventListener("focusin", onFocus);
  document.addEventListener("focusout", onBlur);
  return () => {
    point(null);
    document.removeEventListener("pointerover", onPointer, capture);
    document.removeEventListener("pointerdown", onPointer, capture);
    document.removeEventListener("pointerup", onPointer, capture);
    document.removeEventListener("pointerout", onOut, capture);
    document.removeEventListener("focusin", onFocus);
    document.removeEventListener("focusout", onBlur);
  };
}
