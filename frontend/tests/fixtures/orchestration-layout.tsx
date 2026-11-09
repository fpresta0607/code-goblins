import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";
import { RESTING_AFK_TOGGLE } from "./resting-afk-toggle";

// The canvas the Overlord showed on 2026-10-01: eight goblins under the CEO in
// their order there, cg-board-theme waiting on cg-ceo-wakes. The test puts his
// saved layout in the browser before the page loads.
const fleet = ["cg-native-desktop", "cg-board-theme", "cg-ceo-wakes", "cg-hidden-windows", "cg-review-editor-sync", "cg-credential-requests", "pd-connect-quickstart", "cg-install-no-mistakes-pinned"];
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", tasks: fleet.map((id) => ({
  id, title: id, project: "code-goblins", generation: "1", verified: false,
  ...(id === "cg-board-theme" ? { phase: "waiting", waiting_on: "cg-ceo-wakes" } : { phase: "working" }),
})) });

createRoot(document.getElementById("root")!).render(<main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
  <Orchestration afkToggle={RESTING_AFK_TOGGLE} onOpenCeo={() => {}} snapshot={snapshot} selected="" connected effects={[]} presentations={[]} now={Date.parse("2026-10-01T12:00:00Z")} freshNow={Date.parse("2026-10-01T12:00:00Z")} onSelect={() => {}} onChild={() => {}} />
</main>);
