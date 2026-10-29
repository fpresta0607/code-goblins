// The panel's top row holds its controls and its corner button, Close or
// Back, on one line whatever the panel's width. This is its pure fitting step.

// What the row measures of itself, in CSS pixels: its own width, the corner
// button, one control, the gap between buttons, and how many controls it has.
export interface RowSizes { width: number; corner: number; control: number; gap: number; count: number }

// Controls that cannot fit go into More, which takes one control's room.
// The most important controls stay, and the corner button always shows. A row
// too narrow even for More keeps a lone control in its place, the room More
// would take, and puts two or more into More.
export function rowFit({ width, corner, control, gap, count }: RowSizes): { shown: number } {
  for (let shown = count; shown >= 0; shown--) {
    const buttons = shown + (shown < count ? 1 : 0);
    if (corner + buttons * (control + gap) <= width) return { shown };
  }
  return { shown: count === 1 ? 1 : 0 };
}
