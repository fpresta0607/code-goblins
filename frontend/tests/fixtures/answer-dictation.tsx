import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Command Center with each card that offers the Overlord a box to write
// his own answer in: the CFO's open question, its answered question reopened
// from History to change it, and Bernie's review item.
const NOW = Date.parse("2026-10-13T14:30:00Z");
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60000).toISOString();
const bernie = { id: "cg-question-box", title: "cg-question-box", goblin_name: "Bernie", goblin_title: "Gate Keeper", phase: "working", generation: "s1", verified: false, reported_at: minutesAgo(5) };
const asked = {
  id: "notify-cg-question-box-1", identity: "a".repeat(64), task: "", text: "Should Corey and Densie work with the numbers alone?", options: ["Give them read only access", "Keep the numbers out"], recommended: "Give them read only access",
  created_at: minutesAgo(5), status: "pending",
};
const settled = {
  id: "pp-merge-125-20261013", identity: "f".repeat(64), task: "", text: "Merge PocketPiggies 125?", options: ["Merge it", "Keep it open"], recommended: "Merge it",
  created_at: minutesAgo(70), answered_at: minutesAgo(40), status: "succeeded", answer_id: "board-merge", answer: "Merge it", answer_kind: "option", answered_option: "Merge it", answered_by: "overlord",
};
const review = {
  id: "cg-question-box-review", identity: "c".repeat(64), task: bernie.id, title: "Does the Other box read right?", state: "open", created_at: minutesAgo(3), updated_at: minutesAgo(3),
};
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", revision: 1, cfo_runs: true, actions: [], tasks: [bernie], questions: [asked, settled], reviews: [review] });

function AnswerDictation() {
  return <main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<AnswerDictation />);
