import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window {
    // answerLands is the supervisor taking his answer from the board: every
    // board shows it on its way, as the supervisor records it when it queues.
    answerLands?: (actionID: string, questionID: string, answer: string) => void;
  }
}

// The Command Center as the Overlord goes through it at 12:00Z on 2026-10-12,
// back from AFK mode: three questions of the CFO's that only he can answer.
const NOW = Date.parse("2026-10-12T12:00:00Z");
const ask = (id: string, text: string, options: string[], minutes: number) => ({
  id, identity: "f".repeat(64), task: "", text, options, recommended: options[0], status: "pending", created_at: new Date(NOW - minutes * 60000).toISOString(),
});
const base = {
  healthy: true, instance: "fixture", revision: 1, actions: [] as Record<string, unknown>[], tasks: [], reviews: [],
  questions: [
    ask("afk-left-20261011T042116.474143200Z", "Switch off the 136 live Stripe pilot links?\n- Why it is yours: they take real payments", ["Switch them off now", "Leave them on"], 470),
    ask("afk-left-20261011T054710.635751600Z", "Narrow migration 305's account phase check?\n- Why it is yours: it changes production data", ["Narrow it now", "Leave it as it is"], 400),
    ask("afk-left-20261011T082811.339143300Z", "Push the release tag v0.5.15?\n- Why it is yours: a release is yours", ["Push the tag", "Wait for the next train"], 210),
  ] as Record<string, unknown>[],
};

let raw = base;

function QueueBack() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(raw));
  window.answerLands = (actionID, questionID, answer) => {
    raw = {
      ...raw, revision: raw.revision + 1,
      actions: [...raw.actions, { id: actionID, kind: "cfo_answer", status: "queued", question_id: questionID, text: answer, answer_kind: "option" }],
      questions: raw.questions.map((question) => question.id !== questionID ? question : { ...question, status: "queued", answer_id: actionID, answer, answer_kind: "option" }),
    };
    setSnapshot(parseSnapshot(raw));
  };
  return <main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<QueueBack />);
