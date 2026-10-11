import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CfoPin } from "../../src/CfoPin";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Command Center at 01:50Z on 2026-10-11 as the Overlord's screenshot
// showed it: Tad's review page for the AFK report, under its task's long
// title, and the CFO's picture of the board fix. ?item=picture opens the
// CFO's item instead of Tad's.
const NOW = Date.parse("2026-10-11T01:50:00Z");
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60000).toISOString();
const tad = {
  id: "cg-afk-report-one-merged", title: "The AFK report shows one Merged group, a line the CFO wrote for a landing folds into the merge it names, and nothing that landed carries a warning (Claude Code)",
  goblin_name: "Tad", goblin_title: "Report Fixer", phase: "working", generation: "s1", verified: false, reported_at: minutesAgo(10),
};
const report = {
  id: "cg-afk-report-one-merged", identity: "a".repeat(64), task: tad.id, title: "Your AFK report with one Merged group: before and after pictures",
  lavish: "http://127.0.0.1:4387/session/afk-report", lavish_page: "C:/pages/afk-report.html", state: "open", created_at: minutesAgo(10), updated_at: minutesAgo(10),
};
const picture = {
  id: "board-card-text-fits-20261010", identity: "f".repeat(64), task: "", title: "The orchestration card, after the fit",
  lavish: "http://127.0.0.1:4387/session/after-card", lavish_page: "C:/pages/after-card/page.html", state: "open", created_at: minutesAgo(3), updated_at: minutesAgo(3),
};
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", revision: 1, cfo_runs: true, cfo_harness: "claude", sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }], actions: [], tasks: [tad], questions: [], reviews: [report, picture] });
const isBox = new URLSearchParams(location.search).get("item") === "box";
const shown = new URLSearchParams(location.search).get("item") === "picture" ? picture : report;

function ReviewCardLook() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  useEffect(() => { if (!isBox) setFocus({ key: "review:" + shown.id, at: 1 }); }, []);
  return <main style={{ display: "grid", alignContent: "start", gap: 28, padding: 24 }}>
    {isBox && <section className="task-board" aria-label="Task board"><CfoPin snapshot={snapshot} now={NOW} onOpen={() => {}} onCommand={() => {}} onStart={() => {}} /></section>}
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<ReviewCardLook />);
