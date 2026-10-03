// The panel's top row holds the Task and Terminal switch, its controls and
// its corner button, Close or Back, on one line whatever the panel's width.
// This is the pure step of fitting it.

// What the row measures of itself, in CSS pixels: its own width, the switch
// with its words and with its icons alone (both 0 for a panel with no
// switch), the corner button, one control, the gap between any two things in
// the row, and how many controls there are.
export interface RowSizes { width: number; switchWords: number; switchIcons: number; corner: number; control: number; gap: number; count: number }

// How the row is laid out: how many of its controls stay in it, the most
// important first, and whether the switch keeps its words. The switch and the
// corner button always show. A control the row cannot hold at full size goes
// into More, which takes a control's room itself; the switch gives up its
// words only when the row cannot hold them even with every control in More,
// and with its icons alone the row then holds as many controls as fit.
export function rowFit(sizes: RowSizes): { shown: number; hasWords: boolean } {
  const { width, corner, control, gap, count } = sizes;
  // The most controls that fit beside a switch this wide, with More when any
  // is left out; -1 when not even More fits.
  const fitting = (pill: number) => {
    for (let shown = count; shown >= 0; shown--) {
      const buttons = shown + (shown < count ? 1 : 0);
      if (pill + 2 * gap + corner + buttons * (control + gap) <= width) return shown;
    }
    return -1;
  };
  const withWords = fitting(sizes.switchWords);
  if (withWords >= 0) return { shown: withWords, hasWords: true };
  return { shown: Math.max(0, fitting(sizes.switchIcons)), hasWords: false };
}
