import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Alerts } from "../../src/Alerts";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// One goblin asking a question, served as the supervisor served it on
// 2026-10-01: its blocked notify sets its task blocked, Waiting on the CFO,
// and the question is the CFO's to answer. The test steps the stream with
// window.step: "asked" for the goblin's question, "cfoAsks" for a question
// the CFO has for the Overlord, "restarting" for a supervisor restart that
// drops every task and item, "working" for the goblin back at work, and
// "finished" or "shipped" for its pull request 240 or 241 ready. What an
// alert opens is written in the output and opens the real Command Center.
const asked = "May I finish the remaining heavy steps now, or stay paused?";
const base = { healthy: true, cfo_runs: true };
const working = { id: "cg-board-theme", title: "cg-board-theme", project: "code-goblins", phase: "working", report: "working", activity: "working: building", generation: "s1", verified: false };
const blocked = { ...working, phase: "blocked", report: "blocked", reason: "Waiting on the CFO: " + asked, activity: asked + " options: Finish it now (Recommended) | Stay paused until October 6" };
const question = { id: "notify-cg-board-theme-3753", text: asked + "\n- The operator resumed me.", options: ["Finish it now", "Stay paused until October 6"], recommended: "Finish it now", status: "pending", task: "cg-board-theme", generation: "s1", created_at: "2026-10-01T12:14:58Z" };
const cfoQuestion = { id: "merge-240", identity: "cfo-1", text: "Merge pull request 240 now?\n- Its checks are green.", options: ["Merge it", "Hold it"], recommended: "Merge it", status: "pending", task: "", created_at: "2026-10-01T12:20:00Z" };
const snapshots = {
  working: parseSnapshot({ ...base, instance: "one", revision: 1, tasks: [working] }),
  asked: parseSnapshot({ ...base, instance: "one", revision: 2, tasks: [blocked], questions: [question] }),
  cfoAsks: parseSnapshot({ ...base, instance: "one", revision: 3, tasks: [working], questions: [cfoQuestion] }),
  restarting: parseSnapshot({ ...base, instance: "two", revision: 1, tasks: [] }),
  finished: parseSnapshot({ ...base, instance: "two", revision: 2, tasks: [{ ...working, phase: "review", report: "done", pr: "https://github.com/fpresta0607/code-goblins/pull/240" }] }),
  shipped: parseSnapshot({ ...base, instance: "two", revision: 3, tasks: [{ ...working, phase: "review", report: "done", pr: "https://github.com/fpresta0607/code-goblins/pull/241" }] }),
};
const ignore = () => {};

declare global { interface Window { step: (name: keyof typeof snapshots) => void } }

function Page() {
  const [said, setSaid] = useState("");
  const [shown, setShown] = useState<keyof typeof snapshots>("working");
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const snapshot = snapshots[shown];
  useEffect(() => { window.step = setShown; }, []);
  return <main data-step={shown} style={{ minHeight: "100vh", padding: 24 }}>
    <output aria-label="Opened">{said}</output>
    <Alerts snapshot={snapshot} onOpen={(target) => {
      setSaid("opened " + (target.kind === "command" ? "the Command Center at " + target.key : target.id));
      if (target.kind === "command") setFocus({ key: target.key, at: Date.now() });
    }} />
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
