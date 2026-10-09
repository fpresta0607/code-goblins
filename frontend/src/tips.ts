// The room between a tip and its part, which its arrow spans, and at the
// screen's edges.
const GAP = 8;
// How close to a tip's corner its arrow may sit, clear of the rounding.
const ARROW_INSET = 14;
// How many frames in a row a part must stand in one place before its tip
// stops being drawn again.
const STILL_FRAMES = 3;

// The sides of its part a tip tries, in order.
const SIDES = ["above", "below", "right", "left"] as const;
type Side = typeof SIDES[number];

// Puts a tip against its part: above it, else below it, else to its right,
// else to its left, on the first side where the whole tip fits on the
// screen. Along that side it is centered on the part and kept on the screen,
// and its arrow points at the part's middle. A tip that fits on no side goes
// below its part, kept on the screen. Returns where the part stands.
function place(node: HTMLElement, part: HTMLElement): DOMRect {
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
  return anchor;
}

// Shows the board's tips until the function it returns is called. Every part
// that carries data-tip shows that text in one tip floating over the whole
// page, on the frame the pointer or the keyboard's focus comes to the part,
// for as long as either stays, so no scrolling box clips a tip and every tip
// can be kept on the screen beside its part (see place). While a part is
// pointed at, its tip says what its part's data-tip says now and follows its
// part: it is drawn again when the page scrolls, resizes or changes, and on
// every frame for as long as the part itself moves, as a card lifting under
// the pointer does. A tip beside a part at rest asks for no frames. It goes
// on the frame the part is left, has no tip or has left the page, and at any
// press. A task card gains its tip, its shortened title in full, only once it
// is pointed at, so it counts as a part from the start. A part inside a modal
// dialog shows its tip inside the dialog, which is drawn over the rest of the
// page. A card being dragged shows no tip.
export function watchTips(): () => void {
  let part: HTMLElement | null = null, node: HTMLElement | null = null, frame = 0, still = 0, stood = "";
  // Draws the tip against its part as both are now, and again on the next
  // frame until the part has stood in one place for STILL_FRAMES of them.
  const draw = () => {
    cancelAnimationFrame(frame);
    if (part && !part.isConnected) return point(null);
    const text = part && !part.closest(".sorting") ? part.getAttribute("data-tip") : null;
    if (!part || !text) {
      sized.disconnect();
      node?.remove();
      node = null;
      return;
    }
    if (!node) {
      node = document.createElement("div");
      node.className = "tip";
      node.setAttribute("role", "tooltip");
      sized.observe(node);
    }
    const host = part.closest("dialog") ?? document.body;
    if (node.parentElement !== host) host.append(node);
    if (node.textContent !== text) node.textContent = text;
    const { left, top, width, height } = place(node, part);
    const at = [left, top, width, height].join();
    still = at === stood ? still + 1 : 0;
    stood = at;
    if (still < STILL_FRAMES) frame = requestAnimationFrame(draw);
  };
  // Draws the tip afresh, since its part may have moved or changed.
  const show = () => {
    stood = "";
    draw();
  };
  // What wakes a tip at rest: any change to the page but the tip's own,
  // watched only while a part is pointed at, and the tip's own box changing
  // size, as it does when its font arrives after its text.
  const changes = new MutationObserver((records) => { if (records.some((record) => record.target !== node)) show(); });
  const sized = new ResizeObserver(show);
  const partOf = (target: EventTarget | null) => target instanceof Element ? target.closest<HTMLElement>("[data-tip], .task-card") : null;
  const point = (next: HTMLElement | null) => {
    if (next === part) return;
    part = next;
    changes.disconnect();
    if (part) changes.observe(document.body, { subtree: true, childList: true, attributes: true, characterData: true });
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
  addEventListener("scroll", show, { capture: true, passive: true });
  addEventListener("resize", show);
  return () => {
    point(null);
    document.removeEventListener("pointerover", onPointer, capture);
    document.removeEventListener("pointerdown", onPointer, capture);
    document.removeEventListener("pointerup", onPointer, capture);
    document.removeEventListener("pointerout", onOut, capture);
    document.removeEventListener("focusin", onFocus);
    document.removeEventListener("focusout", onBlur);
    removeEventListener("scroll", show, { capture: true });
    removeEventListener("resize", show);
  };
}
