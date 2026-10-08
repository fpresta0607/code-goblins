import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Alerts } from "../../src/Alerts";
import { CfoPin } from "../../src/CfoPin";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { watchTips } from "../../src/tips";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The CFO's banner while something waits and while all is quiet, and the
// board's alerts after a goblin asks the CFO, one finishes, one fails and the
// CFO asks the Overlord, which brings the one thing alerts show on the
// board: the ask for Windows notifications. What each action would open is
// written under the banners, and Open Command Center opens the real Command
// Center on a review waiting in it.
// The waiting banner sits over a board column, as it does on the board.
const base = { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }] };
const task = (id: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase: "working", generation: id + "-1", verified: false, ...fields });
const quiet = parseSnapshot({ ...base, tasks: [task("cg-board-kill"), task("pd-billing-admin"), task("cg-voice")] });
const question = { id: "q1", text: "Which layout should I keep?", status: "pending", identity: "cfo-1", created_at: "2026-09-30T12:00:00Z" };
const asking = parseSnapshot({ ...base, tasks: quiet.tasks, questions: [question] });
const later = parseSnapshot({ ...base, questions: [{ ...question, task: "cg-voice" }, { id: "q2", text: "Merge pull request 204 now?", status: "pending", identity: "cfo-1", created_at: "2026-09-30T12:05:00Z" }], tasks: [
  task("cg-board-kill", { phase: "done", pr: "https://github.com/fpresta0607/code-goblins/pull/204" }),
  task("pd-billing-admin", { phase: "failed", reason: "its checks failed" }),
  task("cg-voice", { phase: "failed", report: "failed", reason: "Waiting on the CFO: " + question.text, activity: question.text }),
] });
const reviewing = parseSnapshot({ ...base, tasks: quiet.tasks, reviews: [{ id: "r1", task: "cg-voice", title: "Pick the waveform", state: "open", created_at: "2026-09-30T12:00:00Z" }] });
const ignore = () => {};

function Page() {
  const [said, setSaid] = useState("");
  const [snapshot, setSnapshot] = useState(quiet);
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  useEffect(() => { const timer = setTimeout(() => setSnapshot(later), 50); return () => clearTimeout(timer); }, []);
  const terminal = (source: HTMLElement) => setSaid("opened the CFO's terminal from its " + (source.className.includes("portrait") ? "portrait" : "icon"));
  const center = () => { setSaid("opened the Command Center"); setFocus({ key: "", at: Date.now() }); };
  return <main style={{ display: "grid", alignContent: "start", gap: 28, minHeight: "100vh", boxSizing: "border-box", padding: 24, background: "linear-gradient(#07101565, #07101565), url('/assets/goblin-workshop.png') right bottom / cover" }}>
    <section className="task-board" aria-label="Task board">
      <CfoPin snapshot={asking} now={Date.now()} onOpen={terminal} onCommand={center} onStart={() => setSaid("started the CFO")} />
      <section className="board-column" aria-label="Working"><h2>Working</h2></section>
    </section>
    <CfoPin snapshot={quiet} now={Date.now()} onOpen={terminal} onCommand={center} onStart={() => setSaid("started the CFO")} />
    <output aria-label="Opened">{said}</output>
    <Alerts snapshot={snapshot} onOpen={(key) => setSaid("opened the Command Center at " + key)} />
    <CommandCenter snapshot={reviewing} connected presentations={[]} focus={focus} onUnsent={ignore} />
  </main>;
}

watchTips();
createRoot(document.getElementById("root")!).render(<Page />);
