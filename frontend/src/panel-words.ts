// A part of what a harness's own screen asks or shows, as the chat draws it:
// its title, a paragraph of its words, a block of code or a command, or a
// break between them.
export type PanelPart = { kind: "title" | "prose" | "code"; text: string } | { kind: "break" };

// The bars a harness draws down a pane's, a preview's or a quoted command's
// sides, and the rows of its edges, which are the terminal's and never the
// words.
const GUTTER = /^[│┃║▌▍▎▏╎╏┆┇┊┋]\s?|\s?[│┃║▌▍▎▏╎╏┆┇┊┋]$/g;
const EDGE = /^\s*[─╌━═╭╮╰╯┌┐└┘▔▁][─╌━═╭╮╰╯┌┐└┘▔▁\s]*$/;
// A rule of dashes alone, as a harness draws above and below a preview.
const RULE = /^\s*[─╌━═]+\s*$/;
// A pane's top or bottom edge, a corner at each end, as wide as the pane: its
// words wrap at that width less its corners and the space inside each bar.
const PANE_EDGE = /^[╭┌╰└][─━═]+[╮┐╯┘]$/;

const indentOf = (row: string) => row.length - row.trimStart().length;

// A character the terminal draws two cells wide, East Asian wide or fullwidth
// or an emoji shown as a picture, and a mark it draws over the character
// before it in no cell of its own, as a combining accent or a variation
// selector, as the relay measures them.
const WIDE = /[\u1100-\u115F\u2E80-\u303E\u3041-\u33FF\u3400-\u4DBF\u4E00-\u9FFF\uA000-\uA4CF\uA960-\uA97F\uAC00-\uD7A3\uF900-\uFAFF\uFE10-\uFE19\uFE30-\uFE6F\uFF00-\uFF60\uFFE0-\uFFE6\u{16FE0}-\u{18D08}\u{1AFF0}-\u{1B2FF}\u{20000}-\u{3FFFD}]|\p{Emoji_Presentation}/u;
const MARK = /[\p{Mn}\p{Me}\p{Cf}]/u;

// cellsOf is how many of the terminal's cells text takes, the unit cols counts.
const cellsOf = (text: string) => [...text].reduce((cells, char) => cells + (MARK.test(char) ? 0 : WIDE.test(char) ? 2 : 1), 0);

// panelParts reads a panel's rows, as the terminal drew them, as the chat's
// parts (#2223): a pane's gutter bars and edges go; rows indented under the
// words, a command after $, rows between two rules of dashes, such as the
// command a permission prompt quotes or a preview's code, and the indented
// rows above a lone closing rule, a preview the relay cut at its top, are code
// kept line for line; the first row is the title, alone; and a row joins the
// row above it only where the terminal wrapped it, its first word too long
// for the room the row above left at the width its words wrap at, in the
// terminal's cells: a pane's own, else cols, the terminal's less the indent
// the relay took off. With neither known no row joins.
export function panelParts(rows: readonly string[], cols?: number): PanelPart[] {
  const plain = rows.map((row) => row.replace(GUTTER, "").trimEnd());
  const edge = rows.map((row) => row.trim()).find((row) => PANE_EDGE.test(row));
  const width = edge ? edge.length - 4 : cols;
  const rules = plain.flatMap((row, at) => RULE.test(row) ? [at] : []);
  const isCut = rules.length % 2 === 1 && rules[0] > 0;
  const cutFrom = isCut ? plain.findIndex((row) => indentOf(row) > 0) : -1;
  const fences = isCut ? rules.slice(1) : rules;
  const isFenced = (at: number) => (cutFrom >= 0 && cutFrom <= at && at < rules[0])
    || fences.some((rule, index) => index % 2 === 0 && index + 1 < fences.length && rule < at && at < fences[index + 1]);
  const parts: PanelPart[] = [];
  let code: string[] = [];
  let prose: string[] = [];
  const close = () => {
    if (prose.length) parts.push({ kind: "prose", text: prose.join(" ") });
    if (code.length) {
      if (parts.at(-1)?.kind === "break") parts.pop();
      const shared = Math.min(...code.filter((row) => row.trim()).map(indentOf));
      parts.push({ kind: "code", text: code.map((row) => row.slice(shared)).join("\n").replace(/\n+$/, "") });
    }
    prose = [];
    code = [];
  };
  plain.forEach((row, at) => {
    if (EDGE.test(row)) {
      close();
      const last = parts.at(-1)?.kind;
      if (last === "title" || last === "prose") parts.push({ kind: "break" });
    } else if (!row.trim()) {
      if (code.length) code.push("");
      else close();
    } else if (isFenced(at) || indentOf(row) >= 2 || row.startsWith("$ ")) {
      if (prose.length) close();
      code.push(row);
    } else {
      if (code.length) close();
      const words = row.trim();
      if (!parts.length) parts.push({ kind: "title", text: words });
      else {
        if (prose.length && (width === undefined || cellsOf(plain[at - 1]) + 1 + cellsOf(words.split(" ")[0]) <= width)) close();
        prose.push(words);
      }
    }
  });
  close();
  while (parts.length && parts[parts.length - 1].kind === "break") parts.pop();
  return parts;
}
