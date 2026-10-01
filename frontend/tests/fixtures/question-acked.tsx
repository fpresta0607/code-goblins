import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { ackByCFO?: () => void }
}

// A goblin asks the Overlord a question, he answers it in chat, and the CFO
// relays his answer with cfo send and retires the notify with --ack-blocking.
// The question opens the Command Center by itself; ackByCFO is the supervisor
// closing the question once its notify left the wake queue.
const at = "2026-10-01T13:20:00Z";
const question = {
  id: "notify-cg-board-theme-3640", identity: "a".repeat(64), task: "cg-board-theme", generation: "1", seq: 3640,
  text: "Which accent should the board use?", options: ["Mint", "Amber"], recommended: "Mint", status: "pending", created_at: at,
};
const base = {
  healthy: true, instance: "fixture", revision: 1,
  tasks: [{ id: "cg-board-theme", title: "Restyle the board", phase: "blocked", generation: "1", verified: false }],
  questions: [question],
};
const acked = { ...base, revision: 2, questions: [{ ...question, status: "succeeded", message: "Answered by the CFO.", answered_by: "cfo", answered_at: "2026-10-01T13:32:00Z" }] };

function QuestionAcked() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  window.ackByCFO = () => setSnapshot(parseSnapshot(acked));
  return <main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<QuestionAcked />);
