import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window {
    changeLands?: (actionID: string, questionID: string, answer: string) => void;
    goblinReports?: () => void;
  }
}

// History as the Overlord reads it at 02:20Z on 2026-10-06: cg-verify-fast's
// question the CFO answered two minutes ago, which he can still change; his
// own answer; the CFO's answer while he was away, whose goblin has reported
// since; and one whose goblin restarted. changeLands is the supervisor
// delivering his change, and goblinReports cg-verify-fast reporting again.
const minutesAgo = (minutes: number) => new Date(Date.parse("2026-10-06T02:20:00Z") - minutes * 60000).toISOString();
const options = ["Start PR 2's gate now", "Wait for the running test step to end"];
const cfoAnswer = (answer: string, reason = "") => ({ status: "succeeded", answer: reason ? answer + ". " + reason : answer, answer_kind: "option", answered_option: answer, answered_by: "cfo" });
const gate = {
  id: "notify-cg-verify-fast-12", identity: "a".repeat(64), task: "cg-verify-fast", generation: "s1", text: "May PR 2's gate start now?", options, recommended: options[1],
  created_at: minutesAgo(5), answered_at: minutesAgo(2), ...cfoAnswer(options[1], "the running test step ends in 4 minutes"),
};
const accent = {
  id: "notify-cg-board-theme-3", identity: "b".repeat(64), task: "cg-board-theme", generation: "s1", text: "Which accent should the board use?", options: ["Mint", "Amber"],
  created_at: minutesAgo(30), answered_at: minutesAgo(12), status: "succeeded", answer_id: "board-1", answer: "Mint", answer_kind: "option", answered_option: "Mint", answered_by: "overlord",
};
const stray = {
  id: "notify-cg-tidy-home-4", identity: "c".repeat(64), task: "cg-tidy-home", generation: "s1", text: "Remove the three stray worktrees?", options: ["Remove them", "Keep them"],
  created_at: minutesAgo(400), answered_at: minutesAgo(360), ...cfoAnswer("Keep them"), answered_away: true,
};
const older = {
  id: "notify-cg-verify-fast-9", identity: "d".repeat(64), task: "cg-verify-fast", generation: "s0", text: "May PR 1's gate start now?", options: ["Start it now", "Wait"],
  created_at: minutesAgo(700), answered_at: minutesAgo(660), ...cfoAnswer("Start it now"),
};
const task = (id: string, reported: number) => ({ id, title: id, phase: "working", generation: "s1", verified: false, reported_at: minutesAgo(reported) });
const base = {
  healthy: true, instance: "fixture", revision: 1, actions: [] as unknown[],
  tasks: [task("cg-verify-fast", 5), task("cg-board-theme", 1), task("cg-tidy-home", 300)],
  questions: [gate, accent, stray, older] as Record<string, unknown>[],
};

let raw = base;

function AnswerHistory() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(raw));
  const next = (changes: Partial<typeof base>) => { raw = { ...raw, ...changes, revision: raw.revision + 1 }; setSnapshot(parseSnapshot(raw)); };
  window.changeLands = (actionID, questionID, answer) => next({
    actions: [{ id: actionID, kind: "answer_change", status: "succeeded", question_id: questionID }],
    questions: raw.questions.map((question) => question.id !== questionID ? question
      : { ...question, change_id: actionID, answer, answered_option: answer, answered_by: "overlord", replaced_answer: question.answered_option }),
  });
  window.goblinReports = () => next({ tasks: [task("cg-verify-fast", 0), task("cg-board-theme", 1), task("cg-tidy-home", 300)] });
  return <main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<AnswerHistory />);
