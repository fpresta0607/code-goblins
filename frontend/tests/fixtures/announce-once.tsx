import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Alerts } from "../../src/Alerts";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The board's alerts and its Command Center over one supervisor's stream. The
// test steps the stream with window.advance: "asked" for a question the CFO
// has for the Overlord, "answered" for that question answered elsewhere,
// "both" for it beside a second one, "second" for the first answered and the
// second still waiting, "goblinAsks" for a goblin's question to the CFO,
// "restarting" for a supervisor restart that drops every task and item, and
// "republished" for the first question's ID asked again days after its
// answered record was dropped.
// window.focusOn is what a click on a Windows notification asks of the
// Command Center.
const asked = "May the goblin finish the remaining heavy steps now, or stay paused?";
const base = { healthy: true, cfo_runs: true };
const working = { id: "cg-board-theme", title: "cg-board-theme", project: "code-goblins", phase: "working", report: "working", activity: "working: building", generation: "s1", verified: false };
const blocked = { ...working, phase: "blocked", report: "blocked", reason: "Waiting on the CFO: Which port?", activity: "Which port? options: 4310 (Recommended) | 4311" };
const question = { id: "finish-heavy-steps", identity: "c".repeat(64), task: "", text: asked, options: ["Finish it now", "Stay paused until October 6"], recommended: "Finish it now", status: "pending", created_at: "2026-10-01T12:14:58Z" };
const answered = { ...question, status: "succeeded", answered_by: "overlord", answered_in: "chat", answered_option: "Finish it now", answered_at: "2026-10-01T12:16:00Z" };
const second = { id: "merge-freeze", identity: "c".repeat(64), task: "", text: "Lift the merge freeze?", options: ["Lift it", "Keep it"], recommended: "Lift it", status: "pending", created_at: "2026-10-01T12:20:00Z" };
const republished = { ...question, text: "May the goblin finish the next heavy steps now, or stay paused?", created_at: "2026-10-12T09:00:00Z" };
const goblinAsks = { id: "notify-cg-board-theme-3753", text: "Which port?", options: ["4310", "4311"], recommended: "4310", status: "pending", task: "cg-board-theme", generation: "s1", created_at: "2026-10-01T12:25:00Z" };
const snapshots = {
  working: parseSnapshot({ ...base, instance: "one", revision: 1, tasks: [working] }),
  asked: parseSnapshot({ ...base, instance: "one", revision: 2, tasks: [working], questions: [question] }),
  answered: parseSnapshot({ ...base, instance: "one", revision: 3, tasks: [working], questions: [answered] }),
  both: parseSnapshot({ ...base, instance: "one", revision: 4, tasks: [working], questions: [question, second] }),
  second: parseSnapshot({ ...base, instance: "one", revision: 5, tasks: [working], questions: [answered, second] }),
  goblinAsks: parseSnapshot({ ...base, instance: "one", revision: 6, tasks: [blocked], questions: [goblinAsks] }),
  restarting: parseSnapshot({ ...base, instance: "two", revision: 1, tasks: [] }),
  quietCFO: parseSnapshot({ ...base, instance: "one", revision: 7, tasks: [blocked], questions: [goblinAsks], cfo_quiet: { since: "2026-10-02T12:10:00Z", count: 3, oldest_age: 660 } }),
  quietChanged: parseSnapshot({ ...base, instance: "one", revision: 8, tasks: [blocked], questions: [goblinAsks], cfo_quiet: { since: "2026-10-02T12:10:00Z", count: 2, oldest_age: 720 } }),
  republished: parseSnapshot({ ...base, instance: "one", revision: 9, tasks: [working], questions: [republished] }),
  quietAgain: parseSnapshot({ ...base, instance: "two", revision: 2, tasks: [blocked], questions: [goblinAsks], cfo_quiet: { since: "2026-10-02T13:10:00Z", count: 1, oldest_age: 600 } }),
};
const ignore = () => {};

declare global { interface Window { advance: (name: keyof typeof snapshots) => void; focusOn: (key: string) => void } }

function Page() {
  const [shown, setShown] = useState<keyof typeof snapshots>("working");
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const [said, setSaid] = useState("");
  const snapshot = snapshots[shown];
  useEffect(() => { window.advance = setShown; window.focusOn = (key) => setFocus({ key, at: Date.now() }); }, []);
  return <main data-step={shown} style={{ minHeight: "100vh", padding: 24 }}>
    <output>{said}</output>
    <Alerts snapshot={snapshot} onOpen={(target) => { setSaid("opened " + target.kind); if (target.kind === "command") setFocus({ key: target.key, at: Date.now() }); }} />
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
