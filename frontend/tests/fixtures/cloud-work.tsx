import { createRoot } from "react-dom/client";
import { CommandCenterMenu } from "../../src/command-center-menu";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A goblin handed off to a cloud session, paused for the handoff, with a
// teammate's pull request into its branch that the supervisor saw, and one he
// left, which waits on him no more.
const goblin = { id: "cg-parser", title: "Parse headers", project: "code-goblins", phase: "paused", generation: "s1", verified: false, cloud_handoff: { pull_request: "https://github.com/o/r/pull/209", cloud: [
  { pull_request: "https://github.com/o/r/pull/210", title: "Name the bad header", author: "teammate", head: "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00", is_teammates: true, seen_at: "2026-11-25T19:00:00Z" },
  { pull_request: "https://github.com/o/r/pull/211", title: "An earlier try", author: "o", head: "beef0000beef0000beef0000beef0000beef0000", seen_at: "2026-11-25T18:00:00Z", is_left: true },
] } };
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", ceo_runs: true, revision: 1, tasks: [goblin] });
const ignore = () => {};

createRoot(document.getElementById("root")!).render(<main style={{ minHeight: "100vh", padding: 24 }}>
  <CommandCenterMenu snapshot={snapshot} connected presentations={[]} now={Date.parse("2026-11-25T19:05:00Z")} focus={null} onUnsent={ignore} />
</main>);
