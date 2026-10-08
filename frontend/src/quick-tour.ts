export interface Box { left: number; top: number; width: number; height: number }
export type Side = "left" | "right" | "under" | "over";

// The room the tour's card leaves between itself and its part, and at the
// window's edges.
const GAP = 16;

// The sides tried for a card, its step's own side first: then the opposite
// side, then the others.
const SIDES: Record<Side, Side[]> = {
  left: ["left", "right", "under", "over"],
  right: ["right", "left", "under", "over"],
  under: ["under", "over", "left", "right"],
  over: ["over", "under", "left", "right"],
};

// cardPlace puts the tour's card beside the part of the board its step is
// about, on the first side with room for it, so it never covers that part:
// beside the part centered on its height, or over or under it centered on its
// width. A card with no side free, or with no part on screen, sits centered at
// the window's foot. The card is held inside the window on every side.
export function cardPlace(part: Box | null, card: { width: number; height: number }, view: { width: number; height: number }, side: Side): { left: number; top: number } {
  const held = (left: number, top: number) => ({
    left: Math.max(GAP, Math.min(left, view.width - GAP - card.width)),
    top: Math.max(GAP, Math.min(top, view.height - GAP - card.height)),
  });
  if (!part) return held((view.width - card.width) / 2, view.height - GAP - card.height);
  const middle = { left: part.left + part.width / 2 - card.width / 2, top: part.top + part.height / 2 - card.height / 2 };
  for (const each of SIDES[side]) {
    if (each === "left" && part.left - GAP - card.width >= GAP) return held(part.left - GAP - card.width, middle.top);
    if (each === "right" && part.left + part.width + GAP + card.width <= view.width - GAP) return held(part.left + part.width + GAP, middle.top);
    if (each === "under" && part.top + part.height + GAP + card.height <= view.height - GAP) return held(middle.left, part.top + part.height + GAP);
    if (each === "over" && part.top - GAP - card.height >= GAP) return held(middle.left, part.top - GAP - card.height);
  }
  return held((view.width - card.width) / 2, view.height - GAP - card.height);
}
