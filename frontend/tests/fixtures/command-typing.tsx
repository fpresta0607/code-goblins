import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { ask?: (id: string) => void }
}

// A board with a comment box the Overlord types in. ask is a new question
// the CFO has for him arriving, as the supervisor's snapshot brings it.
const question = (id: string) => ({
  id, identity: "q".repeat(64), task: "", status: "pending", created_at: new Date().toISOString(),
  text: "May I build the layout switch as drawn?", options: ["Build as drawn", "Change it first"], recommended: "Build as drawn",
});
const base = { healthy: true, instance: "fixture", revision: 1, tasks: [{ id: "cg-board-polish", title: "Polish the board", phase: "blocked", generation: "1", verified: false }] };

function CommandTyping() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  window.ask = (id) => setSnapshot((prior) => parseSnapshot({ ...base, revision: prior.revision + 1, questions: [...(prior.questions || []), question(id)] }));
  return <main>
    <label>Comment to the CFO <textarea aria-label="Comment to the CFO" rows={3} /></label>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<CommandTyping />);
