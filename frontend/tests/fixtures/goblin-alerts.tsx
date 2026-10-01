import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Alerts } from "../../src/Alerts";
import { CfoPin } from "../../src/CfoPin";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The CFO's banner while something waits and while all is quiet, and the alert
// stack after a goblin asks, one finishes and one fails. What each action
// would open is written under the banners, and Open Command Center opens the
// real Command Center: on a review waiting in it, or with ?nothing on none.
// The waiting banner sits over a board column, as it does on the board.
const base = { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", sessions: [{ id: "cfo-1", harness: "claude", role: "cfo", model: "claude-opus-5-5" }] };
const task = (id: string, fields: Record<string, unknown> = {}) => ({ id, title: id, project: "code-goblins", phase: "working", generation: id + "-1", verified: false, ...fields });
const quiet = parseSnapshot({ ...base, tasks: [task("cg-board-kill"), task("pd-billing-admin"), task("cg-voice")] });
const asking = parseSnapshot({ ...base, tasks: quiet.tasks, questions: [{ id: "q1", text: "Which layout should I keep?", status: "pending", task: "cg-board-kill", created_at: "2026-09-30T12:00:00Z" }] });
const later = parseSnapshot({ ...base, questions: asking.questions, tasks: [
  task("cg-board-kill", { phase: "done", pr: "https://github.com/fpresta0607/code-goblins/pull/204" }),
  task("pd-billing-admin", { phase: "failed", reason: "its checks failed" }),
  task("cg-voice"),
] });
const reviewing = parseSnapshot({ ...base, tasks: quiet.tasks, reviews: [{ id: "r1", task: "cg-voice", title: "Pick the waveform", state: "open", created_at: "2026-09-30T12:00:00Z" }] });
const command = new URLSearchParams(location.search).has("nothing") ? quiet : reviewing;
const ignore = () => {};

function Page() {
  const [said, setSaid] = useState("");
  const [snapshot, setSnapshot] = useState(quiet);
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  useEffect(() => { const timer = setTimeout(() => setSnapshot(later), 50); return () => clearTimeout(timer); }, []);
  const terminal = (source: HTMLElement) => setSaid("opened the CFO's terminal from its " + (source.classList.contains("dialogue-portrait") ? "portrait" : "icon"));
  const center = () => { setSaid("opened the Command Center"); setFocus({ key: "", at: Date.now() }); };
  return <main style={{ display: "grid", alignContent: "start", gap: 28, minHeight: "100vh", boxSizing: "border-box", padding: 24, background: "linear-gradient(#07101565, #07101565), url('/assets/goblin-workshop.png') right bottom / cover" }}>
    <section className="task-board" aria-label="Task board">
      <CfoPin snapshot={asking} onOpen={terminal} onCommand={center} onStart={() => setSaid("started the CFO")} />
      <section className="board-column" aria-label="Working"><h2>Working</h2></section>
    </section>
    <CfoPin snapshot={quiet} onOpen={terminal} onCommand={center} onStart={() => setSaid("started the CFO")} />
    <output aria-label="Opened">{said}</output>
    <Alerts snapshot={snapshot} onOpen={(target) => setSaid("opened " + (target.kind === "command" ? "the Command Center at " + target.key : target.id))} />
    <CommandCenter snapshot={command} connected presentations={[]} focus={focus} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
