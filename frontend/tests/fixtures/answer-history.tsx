import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window {
    // focusItem opens the Command Center on one item, as a click on its
    // History row or a notification does.
    focusItem?: (key: string) => void;
    // changeLands is the supervisor taking his change: every board shows the
    // new answer at once, and the change goes on its way.
    changeLands?: (actionID: string, questionID: string, answer: string, kind?: string) => void;
    // changeDelivered is the change reaching whoever acts on the answer.
    changeDelivered?: (actionID: string) => void;
  }
}

// The Command Center as the Overlord reads it at 01:30Z on 2026-10-11: his
// own answer to the CFO's question about PocketPiggies 125, the CFO's answer
// to Bernie's gate question two minutes ago, the CFO's answer while he was
// away to Lucy, who has reported since, and the CFO's answer to Marge, whose
// pull request merged since.
const NOW = Date.parse("2026-10-11T01:30:00Z");
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60000).toISOString();
const cfoAnswer = (answer: string, reason = "") => ({ status: "succeeded", answer: reason ? answer + ". " + reason : answer, answer_kind: "option", answered_option: answer, answered_by: "cfo" });
const merge = {
  id: "pp-merge-125-20261010", identity: "f".repeat(64), task: "", text: "Merge PocketPiggies 125?\n- CI is green on current main.", options: ["Merge it", "Keep it open"], recommended: "Merge it",
  created_at: minutesAgo(70), answered_at: minutesAgo(40), status: "succeeded", answer_id: "board-merge", answer: "Merge it", answer_kind: "option", answered_option: "Merge it", answered_by: "overlord",
};
const gate = {
  id: "notify-cg-verify-fast-12", identity: "a".repeat(64), task: "cg-verify-fast", generation: "s1", text: "May PR 2's gate start now?", options: ["Start PR 2's gate now", "Wait for the running test step to end"], recommended: "Wait for the running test step to end",
  created_at: minutesAgo(5), answered_at: minutesAgo(2), ...cfoAnswer("Wait for the running test step to end", "the running test step ends in 4 minutes"),
};
const accent = {
  id: "notify-cg-board-theme-3", identity: "b".repeat(64), task: "cg-board-theme", generation: "s1", text: "Which accent should the board use?", options: ["Mint", "Amber"],
  created_at: minutesAgo(200), answered_at: minutesAgo(180), ...cfoAnswer("Mint"), answered_away: true,
};
const tenants = {
  id: "notify-pd-pilot-sweep-7", identity: "c".repeat(64), task: "pd-pilot-sweep", generation: "s1", text: "Move the three pilot tenants to the free tier?", options: ["Move them now", "Wait for Prometheus"], recommended: "Move them now",
  created_at: minutesAgo(300), answered_at: minutesAgo(290), ...cfoAnswer("Move them now"), answered_away: true,
};
const task = (id: string, name: string, title: string, reported: number, merged = false) => ({ id, title: id, goblin_name: name, goblin_title: title, phase: merged ? "done" : "working", generation: "s1", verified: false, reported_at: minutesAgo(reported), merged });
const base = {
  healthy: true, instance: "fixture", revision: 1, actions: [] as Record<string, unknown>[],
  tasks: [task("cg-verify-fast", "Bernie", "Gate Keeper", 5), task("cg-board-theme", "Lucy", "Pixel Polisher", 20), task("pd-pilot-sweep", "Marge", "Tenant Wrangler", 60, true)],
  questions: [merge, gate, accent, tenants] as Record<string, unknown>[],
  reviews: [] as Record<string, unknown>[],
};

let raw = base;

function AnswerReopen() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(raw));
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const next = (changes: Partial<typeof base>) => { raw = { ...raw, ...changes, revision: raw.revision + 1 }; setSnapshot(parseSnapshot(raw)); };
  window.focusItem = (key) => setFocus({ key, at: Date.now() });
  window.changeLands = (actionID, questionID, answer, kind = "option") => next({
    actions: [...raw.actions, { id: actionID, kind: "answer_change", status: "queued", question_id: questionID, text: answer, answer_kind: kind }],
    questions: raw.questions.map((question) => question.id !== questionID ? question : {
      ...question, change_id: actionID, answer, answer_kind: kind, answered_option: kind === "option" ? answer : "", answered_by: "overlord", answered_at: new Date(Date.now()).toISOString(), answered_away: false,
      replaced_answer: question.answer_kind === "other" ? question.answer : question.answered_option,
    }),
  });
  window.changeDelivered = (actionID) => next({ actions: raw.actions.map((action) => action.id === actionID ? { ...action, status: "succeeded" } : action) });
  return <main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<AnswerReopen />);
