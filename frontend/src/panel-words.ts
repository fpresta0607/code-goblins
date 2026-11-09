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

const indentOf = (row: string) => row.length - row.trimStart().length;

// panelParts reads a panel's rows, as the terminal drew them, as the chat's
// parts (#2223): a pane's gutter bars and edges go; rows indented under the
// words, a command after $, and rows between two rules of dashes, such as the command
// a permission prompt quotes or a preview's code, are code kept line for
// line; and the words between blank rows are one paragraph, the terminal's
// wrap undone, the first of them the title.
export function panelParts(rows: readonly string[]): PanelPart[] {
  const plain = rows.map((row) => row.replace(GUTTER, "").trimEnd());
  const rules = plain.flatMap((row, at) => RULE.test(row) ? [at] : []);
  const isFenced = (at: number) => rules.some((rule, index) => index + 1 < rules.length && rule < at && at < rules[index + 1]);
  const parts: PanelPart[] = [];
  let code: string[] = [];
  let prose: string[] = [];
  const close = () => {
    if (prose.length) parts.push({ kind: parts.some((part) => part.kind !== "break") ? "prose" : "title", text: prose.join(" ") });
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
      prose.push(row.trim());
    }
  });
  close();
  while (parts.length && parts[parts.length - 1].kind === "break") parts.pop();
  return parts;
}
