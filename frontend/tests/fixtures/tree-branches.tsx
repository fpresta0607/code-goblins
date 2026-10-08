import { createRoot } from "react-dom/client";
import { Orchestration } from "../../src/Orchestration";
import { Lineage } from "../../src/Lineage";
import { watchTips } from "../../src/tips";
import { parseSnapshot } from "../../src/types";
import { treeFleet } from "./tree-branches-fleet";
import "../../src/styles.css";

// The fleet of tree-branches-fleet.ts on the canvas, or with ?view=lineage in
// the lineage list the canvas becomes at phone width.
const now = Date.now();
const snapshot = parseSnapshot(treeFleet(now));
const view = new URLSearchParams(location.search).get("view");
watchTips();
createRoot(document.getElementById("root")!).render(view === "lineage"
  ? <main className="canvas-region" style={{ width: "100%" }}><Lineage snapshot={snapshot} project="" selected={null} effects={[]} presentations={[]} now={now} onSelect={() => {}} /></main>
  : <main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
    <Orchestration snapshot={snapshot} selected="" connected effects={[]} presentations={[]} now={now} onSelect={() => {}} onChild={() => {}} />
  </main>);
