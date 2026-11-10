import { test } from "node:test";
import assert from "node:assert/strict";
import { panelParts, type PanelPart } from "./panel-words.ts";

const DASHES = "─".repeat(40);

const pane = (...rows: string[]) => ["╭" + DASHES + "╮", ...rows.map((row) => "│ " + row.padEnd(DASHES.length - 2) + " │"), "╰" + DASHES + "╯"];

const cases: { name: string; rows: string[]; cols?: number; parts: PanelPart[] }[] = [
  {
    name: "a permission prompt's quoted command is code and its words wrapped at 40 cols one paragraph",
    cols: 40,
    rows: ["Bash command", "", "  gh pr checks 2221 --json name,bucket", "  Read the pull request's checks", "", "This command needs approval because it", "reads the network.", "", "Do you want to proceed?"],
    parts: [{ kind: "title", text: "Bash command" }, { kind: "code", text: "gh pr checks 2221 --json name,bucket\nRead the pull request's checks" },
      { kind: "prose", text: "This command needs approval because it reads the network." }, { kind: "prose", text: "Do you want to proceed?" }],
  },
  {
    name: "a pane's gutter bars and edges go, and what lies between two rules of dashes is code",
    rows: ["╭" + DASHES + "╮", "│ Create file                              │", "│ nine.txt                                 │", DASHES, " 1 nine", " 2 ten", DASHES, "Do you want to create nine.txt?", "╰" + DASHES + "╯"],
    parts: [{ kind: "title", text: "Create file" }, { kind: "prose", text: "nine.txt" }, { kind: "code", text: "1 nine\n2 ten" }, { kind: "prose", text: "Do you want to create nine.txt?" }],
  },
  {
    name: "a preview the relay cut at its top, above a lone closing rule, is code and no title",
    rows: [" 27 line", " 28 line", " 29 line", "╌╌╌", "Do you want to create big.txt?"],
    parts: [{ kind: "code", text: "27 line\n28 line\n29 line" }, { kind: "prose", text: "Do you want to create big.txt?" }],
  },
  {
    name: "the model picker's first row is the title alone and the row under it its own paragraph",
    rows: ["Select model", "Switch between Claude models."],
    cols: 80,
    parts: [{ kind: "title", text: "Select model" }, { kind: "prose", text: "Switch between Claude models." }],
  },
  {
    name: "the cost panel keeps its two lines",
    rows: ["Total cost: $0.12", "Total duration: 3m 4s"],
    cols: 80,
    parts: [{ kind: "title", text: "Total cost: $0.12" }, { kind: "prose", text: "Total duration: 3m 4s" }],
  },
  {
    name: "a cost panel at 80 cols keeps its three lines",
    rows: ["Total cost: $0.12", "Total duration (API): 1m 2s", "Total duration (wall): 3m 4s"],
    cols: 80,
    parts: [{ kind: "title", text: "Total cost: $0.12" }, { kind: "prose", text: "Total duration (API): 1m 2s" }, { kind: "prose", text: "Total duration (wall): 3m 4s" }],
  },
  {
    name: "two sentences at 80 cols, the upper leaving room for the next one's first word, stay apart",
    rows: ["Hooks need review", "2 hooks are new or changed.", "Review them before they run."],
    cols: 80,
    parts: [{ kind: "title", text: "Hooks need review" }, { kind: "prose", text: "2 hooks are new or changed." }, { kind: "prose", text: "Review them before they run." }],
  },
  {
    name: "words in a pane join by the pane's width, not the terminal's",
    rows: pane("Bash command", "", "This command needs approval because it", "reads the network."),
    cols: 80,
    parts: [{ kind: "title", text: "Bash command" }, { kind: "prose", text: "This command needs approval because it reads the network." }],
  },
  {
    name: "Codex's question wrapped at 40 columns, sent as 38 with its indent of two taken off, joins",
    cols: 38,
    rows: ["Run this command?", "This command needs approval because it", "reads the network."],
    parts: [{ kind: "title", text: "Run this command?" }, { kind: "prose", text: "This command needs approval because it reads the network." }],
  },
  {
    name: "the same rows with room left for the next row's first word stay apart",
    cols: 44,
    rows: ["Run this command?", "This command needs approval because it", "reads the network."],
    parts: [{ kind: "title", text: "Run this command?" }, { kind: "prose", text: "This command needs approval because it" }, { kind: "prose", text: "reads the network." }],
  },
  {
    name: "a row of wide characters is measured in the cells it takes, so its wrap joins",
    cols: 20,
    rows: ["Title", "\u65e5\u672c\u8a9e\u306e\u8aac\u660e", "approval"],
    parts: [{ kind: "title", text: "Title" }, { kind: "prose", text: "\u65e5\u672c\u8a9e\u306e\u8aac\u660e approval" }],
  },
  {
    name: "a variation selector takes no cell, so a row that left room stays apart",
    cols: 14,
    rows: ["Title", "Saved \u2764\uFE0F done", "x"],
    parts: [{ kind: "title", text: "Title" }, { kind: "prose", text: "Saved \u2764\uFE0F done" }, { kind: "prose", text: "x" }],
  },
  {
    name: "with no pane and no cols known no row joins",
    rows: ["Run it?", "This command needs approval because it", "reads the network."],
    parts: [{ kind: "title", text: "Run it?" }, { kind: "prose", text: "This command needs approval because it" }, { kind: "prose", text: "reads the network." }],
  },
  {
    name: "a row the terminal wrapped joins the row above, and a paragraph's own row stays its own",
    cols: 64,
    rows: ["Switch model?", "Your next response will be slower and use more tokens", "", "This conversation is cached for the current model. Switching to", "Haiku 5.5 means the full history gets re-read."],
    parts: [{ kind: "title", text: "Switch model?" }, { kind: "prose", text: "Your next response will be slower and use more tokens" },
      { kind: "prose", text: "This conversation is cached for the current model. Switching to Haiku 5.5 means the full history gets re-read." }],
  },
  {
    name: "a Codex command after $ is code",
    rows: ["Would you like to run the following command?", "", "$ powershell.exe -NoProfile -Command Get-Date"],
    parts: [{ kind: "title", text: "Would you like to run the following command?" }, { kind: "code", text: "$ powershell.exe -NoProfile -Command Get-Date" }],
  },
  {
    name: "a blank row inside code stays and the shared indent goes",
    rows: ["Edit", "    if (ok) {", "", "      run();", "    }"],
    parts: [{ kind: "title", text: "Edit" }, { kind: "code", text: "if (ok) {\n\n  run();\n}" }],
  },
  { name: "no rows are no parts", rows: [], parts: [] },
  { name: "edges alone are no parts", rows: [DASHES, "", DASHES], parts: [] },
];

for (const { name, rows, cols, parts } of cases) {
  test("panelParts: " + name, () => {
    // Act
    const read = panelParts(rows, cols);

    // Assert
    assert.deepEqual(read, parts);
  });
}

test("panelParts leaves no gutter bar or edge in any part's words", () => {
  // Arrange
  const rows = ["▌ Run the deploy?", "▌", "▌   npm run deploy -- --prod", "┃ It pushes to production. ┃", "╰" + DASHES + "╯"];

  // Act
  const words = panelParts(rows).map((part) => part.kind === "break" ? "" : part.text).join("\n");

  // Assert
  assert.doesNotMatch(words, /[│┃║▌▍▎▏╎╏┆┇┊┋─╭╮╰╯]/);
  assert.match(words, /npm run deploy -- --prod/);
});
