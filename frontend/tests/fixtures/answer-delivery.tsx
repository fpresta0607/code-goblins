import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { settle?: (how: "busy" | "delivered" | "lost", answer: string) => void }
}

// The CFO asks the Overlord a question and he answers it on the board while
// the CFO is inside a turn. settle is the supervisor's next snapshot: the
// answer typed and submitted and waiting for the CFO's hook (busy), the hook
// reporting it taken (delivered), or the CFO never picking it up (lost).
const identity = "c".repeat(64);
const question = {
  id: "freeze-lift", identity, task: "", text: "Lift the merge freeze?", options: ["Lift it", "Keep it"], recommended: "Lift it",
  status: "pending", created_at: "2026-10-01T16:54:00Z",
};
const base = { healthy: true, instance: "fixture", revision: 1, tasks: [], questions: [question], actions: [] };
const advice = "Your answer was typed for the CFO, which has not picked it up. Open its terminal and press Enter if your answer is waiting in its box; if it is not there, type it to the CFO.";
const answered = (status: string, answer: string) => ({ ...question, status, answer_id: answer, answer: "Lift it", answer_kind: "option" });
const action = (answer: string, fields: Record<string, unknown>) => ({ id: answer, kind: "cfo_answer", question_id: question.id, generation: identity, updated_at: "2026-10-01T16:55:01Z", ...fields });
const snapshots = {
  busy: (answer: string) => ({ ...base, revision: 2, questions: [answered("running", answer)],
    actions: [action(answer, { status: "running", message: "Sent. The CFO takes it at its next tool call, or as its current turn ends.", awaiting: { host: "cfo", since: "2026-10-01T16:54:54Z" } })] }),
  delivered: (answer: string) => ({ ...base, revision: 3, questions: [{ ...answered("succeeded", answer), answered_option: "Lift it", answered_by: "overlord", answered_at: "2026-10-01T16:55:20Z" }],
    actions: [action(answer, { status: "succeeded", message: "Taken by the CFO, as its own record of the conversation shows." })] }),
  lost: (answer: string) => ({ ...base, revision: 3, questions: [answered("uncertain", answer)],
    actions: [action(answer, { status: "uncertain", message: advice, advice })] }),
};

function AnswerDelivery() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  window.settle = (how, answer) => setSnapshot(parseSnapshot(snapshots[how](answer)));
  return <main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<AnswerDelivery />);
