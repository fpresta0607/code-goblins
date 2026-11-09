import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenterMenu } from "../../src/command-center-menu";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { ask?: (id: string) => void }
}

// A board with a comment box the Overlord types in. ask is a new question
// the CEO has for him arriving, as the supervisor's snapshot brings it.
const question = (id: string) => ({
  id, identity: "q".repeat(64), task: "", status: "pending", created_at: new Date().toISOString(),
  text: "May I build the layout switch as drawn?", options: ["Build as drawn", "Change it first"], recommended: "Build as drawn",
});
const base = { healthy: true, instance: "fixture", revision: 1, tasks: [{ id: "cg-board-polish", title: "Polish the board", phase: "blocked", generation: "1", verified: false }] };

function CommandTyping() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  window.ask = (id) => setSnapshot((prior) => parseSnapshot({ ...base, revision: prior.revision + 1, questions: [...(prior.questions || []), question(id)] }));
  return <main>
    <label>Comment to the CEO <textarea aria-label="Comment to the CEO" rows={3} /></label>
    <CommandCenterMenu snapshot={snapshot} connected presentations={[]} now={Date.now()} focus={null} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<CommandTyping />);
