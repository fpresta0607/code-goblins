import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The canvas the Overlord showed on 2026-10-01: eight goblins under the CFO in
// their order there, cg-board-theme waiting on cg-cfo-wakes. The test puts his
// saved layout in the browser before the page loads.
const fleet = ["cg-native-desktop", "cg-board-theme", "cg-cfo-wakes", "cg-hidden-windows", "cg-review-editor-sync", "cg-credential-requests", "pd-connect-quickstart", "cg-install-no-mistakes-pinned"];
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", tasks: fleet.map((id) => ({
  id, title: id, project: "code-goblins", generation: "1", verified: false,
  ...(id === "cg-board-theme" ? { phase: "waiting", waiting_on: "cg-cfo-wakes" } : { phase: "working" }),
})) });

createRoot(document.getElementById("root")!).render(<main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
  <Orchestration snapshot={snapshot} selected="" connected effects={[]} presentations={[]} onSelect={() => {}} />
</main>);
