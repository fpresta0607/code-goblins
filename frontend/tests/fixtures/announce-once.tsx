import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Alerts } from "../../src/Alerts";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The board's alerts and its Command Center over one supervisor's stream. The
// test steps the stream with window.advance: "asked" for a goblin's question,
// "answered" for that question answered elsewhere, "both" for it beside a
// question of the CFO's own, "cfoOnly" for the goblin's answered and the
// CFO's still waiting, and "restarting" for a supervisor restart that drops
// every task and item. window.focusOn is what a click on an alert or a
// Windows notification asks of the Command Center.
const asked = "May I finish the remaining heavy steps now, or stay paused?";
const base = { healthy: true, cfo_runs: true };
const working = { id: "cg-board-theme", title: "cg-board-theme", project: "code-goblins", phase: "working", report: "working", activity: "working: building", generation: "s1", verified: false };
const blocked = { ...working, phase: "blocked", report: "blocked", reason: "Waiting on the CFO: " + asked, activity: asked + " options: Finish it now (Recommended) | Stay paused until October 6" };
const question = { id: "notify-cg-board-theme-3753", text: asked, options: ["Finish it now", "Stay paused until October 6"], recommended: "Finish it now", status: "pending", task: "cg-board-theme", generation: "s1", created_at: "2026-10-01T12:14:58Z" };
const answered = { ...question, status: "succeeded", answered_by: "cfo", answered_option: "Finish it now", answered_at: "2026-10-01T12:16:00Z" };
const cfoAsks = { id: "merge-freeze", identity: "c".repeat(64), task: "", text: "Lift the merge freeze?", options: ["Lift it", "Keep it"], recommended: "Lift it", status: "pending", created_at: "2026-10-01T12:20:00Z" };
const snapshots = {
  working: parseSnapshot({ ...base, instance: "one", revision: 1, tasks: [working] }),
  asked: parseSnapshot({ ...base, instance: "one", revision: 2, tasks: [blocked], questions: [question] }),
  answered: parseSnapshot({ ...base, instance: "one", revision: 3, tasks: [working], questions: [answered] }),
  both: parseSnapshot({ ...base, instance: "one", revision: 4, tasks: [blocked], questions: [question, cfoAsks] }),
  cfoOnly: parseSnapshot({ ...base, instance: "one", revision: 5, tasks: [working], questions: [answered, cfoAsks] }),
  restarting: parseSnapshot({ ...base, instance: "two", revision: 1, tasks: [] }),
};
const ignore = () => {};

declare global { interface Window { advance: (name: keyof typeof snapshots) => void; focusOn: (key: string) => void } }

function Page() {
  const [shown, setShown] = useState<keyof typeof snapshots>("working");
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const snapshot = snapshots[shown];
  useEffect(() => { window.advance = setShown; window.focusOn = (key) => setFocus({ key, at: Date.now() }); }, []);
  return <main data-step={shown} style={{ minHeight: "100vh", padding: 24 }}>
    <Alerts snapshot={snapshot} onOpen={(target) => { if (target.kind === "command") setFocus({ key: target.key, at: Date.now() }); }} />
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
