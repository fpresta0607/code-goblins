import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CfoPin } from "../../src/CfoPin";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The CFO's bar over a home whose CFO was closed, with goblins still at work,
// as the board shows it. What its other actions would open is written under
// it; Reopen makes its own request, which each test answers.
const closed = parseSnapshot({ healthy: true, instance: "fixture", cfo_runs: false, cfo_closed: true, tasks: [
  { id: "cg-board-kill", title: "cg-board-kill", project: "code-goblins", phase: "working", generation: "cg-board-kill-1", verified: false },
] });

function Page() {
  const [said, setSaid] = useState("");
  return <main style={{ display: "grid", alignContent: "start", gap: 28, minHeight: "100vh", boxSizing: "border-box", padding: 24 }}>
    <section className="task-board" aria-label="Task board">
      <CfoPin snapshot={closed} onOpen={() => setSaid("opened the CFO's terminal")} onCommand={() => setSaid("opened the Command Center")} onStart={() => setSaid("opened the first-run page")} />
      <section className="board-column" aria-label="Working"><h2>Working</h2></section>
    </section>
    <output aria-label="Opened">{said}</output>
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
