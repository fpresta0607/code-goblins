import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { recordInChat?: () => void }
}

// The CFO's question herdr-strays-20260929 waits on the Overlord, with a
// goblin's question after it. recordInChat is the supervisor taking the CFO's
// record of the answer he gave in chat (2026-09-29).
const at = "2026-09-29T02:00:00Z";
const strays = {
  id: "herdr-strays-20260929", identity: "c".repeat(64), task: "", status: "pending", created_at: at,
  text: "May I stop the 4 stray Herdr panes left from yesterday?", options: ["Stop them", "Leave them"], recommended: "Stop them",
};
const layout = {
  id: "notify-cg-board-polish-7", identity: "d".repeat(64), task: "cg-board-polish", generation: "1", seq: 7, status: "pending", created_at: "2026-09-29T02:05:00Z",
  text: "May I build the layout switch as drawn?", options: ["Build as drawn", "Change it first"], recommended: "Build as drawn",
};
const base = { healthy: true, instance: "fixture", revision: 1, tasks: [{ id: "cg-board-polish", title: "Polish the board", phase: "blocked", generation: "1", verified: false }], questions: [strays, layout] };
const inChat = { ...base, revision: 2, questions: [{ ...strays, status: "succeeded", answer: "Stop them", answer_kind: "option", answered_option: "Stop them", answered_by: "overlord", answered_in: "chat", answered_at: "2026-09-29T02:12:00Z" }, layout] };

function AnswerElsewhere() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  window.recordInChat = () => setSnapshot(parseSnapshot(inChat));
  return <main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<AnswerElsewhere />);
