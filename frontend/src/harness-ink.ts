import { BRAND_MARKS } from "./brandMarks";
import { harnessMark } from "./connectors";

// The colour a session's harness draws its own working state in, taken from
// the board's own marks: a brand mark's ink where the board has one, and the
// tone its glyph mark is drawn in where it has not. Claude Code is its orange,
// Codex the light ink its mark carries, and pi the board's moss (the Overlord,
// 2026-10-26: "it shouldn't all be orange, it should match [the harness]").
export function harnessInk(harness: string): string {
  const mark = harnessMark(harness);
  if ("brand" in mark) return BRAND_MARKS[mark.brand].ink;
  if (harness === "pi") return "#86B36B";
  if (harness === "kimi") return BRAND_MARKS.kimi.ink;
  return BRAND_MARKS.openai.ink;
}
