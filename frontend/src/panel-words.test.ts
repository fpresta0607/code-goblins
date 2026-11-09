import { test } from "node:test";
import assert from "node:assert/strict";
import { panelParts, type PanelPart } from "./panel-words.ts";

const DASHES = "─".repeat(40);

const cases: { name: string; rows: string[]; parts: PanelPart[] }[] = [
  {
    name: "a permission prompt's quoted command is code and its wrapped words one paragraph",
    rows: ["Bash command", "", "  gh pr checks 2221 --json name,bucket", "  Read the pull request's checks", "", "This command needs approval because it", "reads the network.", "", "Do you want to proceed?"],
    parts: [{ kind: "title", text: "Bash command" }, { kind: "code", text: "gh pr checks 2221 --json name,bucket\nRead the pull request's checks" },
      { kind: "prose", text: "This command needs approval because it reads the network." }, { kind: "prose", text: "Do you want to proceed?" }],
  },
  {
    name: "a pane's gutter bars and edges go, and what lies between two rules of dashes is code",
    rows: ["╭" + DASHES + "╮", "│ Create file                              │", "│ nine.txt                                 │", DASHES, " 1 nine", " 2 ten", DASHES, "Do you want to create nine.txt?", "╰" + DASHES + "╯"],
    parts: [{ kind: "title", text: "Create file nine.txt" }, { kind: "code", text: "1 nine\n2 ten" }, { kind: "prose", text: "Do you want to create nine.txt?" }],
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

for (const { name, rows, parts } of cases) {
  test("panelParts: " + name, () => {
    // Act
    const read = panelParts(rows);

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
