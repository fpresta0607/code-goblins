import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A fleet of ?goblins= goblins under the CFO, five by default, for the
// canvas's mouse controls: the wheel, a drag of the canvas, Fit and Arrange.
const count = Number(new URLSearchParams(location.search).get("goblins") || 5);
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", tasks: Array.from({ length: count }, (_, i) => ({
  id: "goblin-" + (i + 1), title: "Goblin " + (i + 1), project: "code-goblins", generation: "1", verified: false, phase: "working",
})) });

createRoot(document.getElementById("root")!).render(<main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
  <Orchestration snapshot={snapshot} selected="" connected effects={[]} presentations={[]} now={Date.parse("2026-10-07T12:00:00Z")} onSelect={() => {}} />
</main>);
