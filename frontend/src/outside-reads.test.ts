import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { test } from "node:test";
import ts from "typescript";

// A pull request's own CI run starts only the jobs its change can alter, and
// the go workflow's JOBS says what each job reads. The browser jobs start for
// a change under frontend, so a spec that reads a file outside it, as two do,
// makes that file one more thing the browser jobs must start for: left off
// the list, a change to it would skip the spec that reads it. This reads the
// source for every path written as one string that leads out of frontend and
// holds the lists to exactly those.
const SOURCE = path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1"));
const FRONTEND = path.dirname(SOURCE);
const REPOSITORY = path.dirname(FRONTEND);
const WORKFLOW = path.join(REPOSITORY, ".github", "workflows", "go.yml");

interface Jobs {
  frontend: { paths: string[] };
  browser: { paths: string[] };
}

// The workflow keeps JOBS as JSON in a block of its env: every line after
// the key that is indented deeper than it.
function jobs(): Jobs {
  const lines = readFileSync(WORKFLOW, "utf8").split(/\r?\n/);
  const start = lines.findIndex((line) => /^ {2}JOBS: \|$/.test(line));
  assert.notEqual(start, -1, "the go workflow has no JOBS in its env");
  const block: string[] = [];
  for (const line of lines.slice(start + 1)) {
    if (line.trim() !== "" && !line.startsWith("    ")) break;
    block.push(line);
  }
  return JSON.parse(block.join("\n")) as Jobs;
}

// Every file outside frontend that the source under folder names by a
// relative path, from the repository root with forward slashes.
function outsideReads(folder: string): string[] {
  const found = new Set<string>();
  for (const name of readdirSync(path.join(FRONTEND, folder), { recursive: true, encoding: "utf8" })) {
    if (!/\.tsx?$/.test(name)) continue;
    const file = path.join(FRONTEND, folder, name);
    const source = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, name.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
    const visit = (node: ts.Node) => {
      if (ts.isStringLiteralLike(node) && node.text.startsWith("../")) {
        const target = path.relative(REPOSITORY, path.resolve(path.dirname(file), node.text)).replaceAll("\\", "/");
        if (target !== "frontend" && !target.startsWith("frontend/") && !target.startsWith("../")) found.add(target);
      }
      ts.forEachChild(node, visit);
    };
    visit(source);
  }
  return [...found].sort();
}

test("the browser jobs start for every file outside frontend that a spec reads, and for no other", () => {
  const listed = jobs().browser.paths.filter((pattern) => !pattern.startsWith("frontend/")).sort();
  assert.deepEqual(outsideReads("tests"), listed, "the browser paths of JOBS in .github/workflows/go.yml and the files outside frontend that the specs read differ: list each such file there, and drop one no spec reads any more");
});

test("the frontend job starts for every file outside frontend that the board's unit tests read", () => {
  // This test reads the workflow, and a change to a workflow starts every
  // job by the rule itself.
  const unlisted = outsideReads("src").filter((file) => !file.startsWith(".github/") && !jobs().frontend.paths.includes(file));
  assert.deepEqual(unlisted, [], "list each of these under the frontend paths of JOBS in .github/workflows/go.yml");
});
