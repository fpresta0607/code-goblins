import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CfoPin } from "../../src/CfoPin";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The CFO's bar over a home whose CFO runs in its native terminal, as the
// board shows it, or, with #starting, one whose CFO has not registered yet.
// What its other actions would open is written under it; Restart makes its
// own request, which each test answers.
const isStarting = location.hash === "#starting";
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", cfo_runs: true, cfo_starting: isStarting, cfo_terminal: "cfo", cfo_terminal_since: "2026-10-05T09:00:00Z", cfo_harness: isStarting ? "" : "claude", tasks: [
  { id: "cg-board-kill", title: "cg-board-kill", project: "code-goblins", phase: "working", generation: "cg-board-kill-1", verified: false },
] });

function Page() {
  const [said, setSaid] = useState("");
  return <main style={{ display: "grid", alignContent: "start", gap: 28, minHeight: "100vh", boxSizing: "border-box", padding: 24 }}>
    <section className="task-board" aria-label="Task board">
      <CfoPin snapshot={snapshot} onOpen={() => setSaid("opened the CFO's terminal")} onCommand={() => setSaid("opened the Command Center")} onStart={() => setSaid("opened the first-run page")} />
      <section className="board-column" aria-label="Working"><h2>Working</h2></section>
    </section>
    <output aria-label="Opened">{said}</output>
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
