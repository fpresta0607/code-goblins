import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { TaskCard } from "../../src/TaskCard";
import { TerminalDeck } from "../../src/TerminalDeck";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { reportSession: (phase: string) => void }
}

const root = createRoot(document.getElementById("root")!);
const parameters = new URLSearchParams(location.search);
function render(phase: string) {
  const isRetired = phase === "retired";
  const isMissing = parameters.has("missing");
  const snapshot = parseSnapshot({
    instance: "ended-proof", healthy: true,
    tasks: [{
      id: isRetired ? "finished:input-proof" : "input-proof", title: "Terminal fixes and handoff", project: "code-goblins",
      backend: "native", harness: "claude", generation: isRetired || phase === "queued" ? "" : "s1", session: "session-proof",
      phase: isRetired ? "done" : phase, archived: isRetired, verified: false,
      runtime: { state: phase === "working" ? "busy" : phase === "done" ? "idle" : phase },
      activity: isMissing ? "" : "Terminal fixes verified. Pull request ready.", report: "done",
      last_report: isMissing ? "" : "Terminal fixes verified. Pull request ready.",
      at: isMissing ? "0001-01-01T00:00:00Z" : "2026-09-29T09:42:00Z", handoff: !isMissing,
      retired_at: isMissing || parameters.has("missing-time") ? "" : "2026-09-29T09:42:00Z",
      pr: "https://github.com/example/project/pull/218",
    }],
    sessions: [{ id: "session-proof", native_id: "b45f3bd2-2b4e-4ee8-8140-3537f8485a36", role: "goblin", task_id: "input-proof", generation: "s1", phase: "working" }],
  });
  const task = snapshot.tasks[0];
  root.render(<main style={{ display: "flex", minHeight: "100vh" }}>
    <aside style={{ width: 300, flex: "none", padding: 16 }}>
      <TaskCard task={task} snapshot={snapshot} selected presentations={[]} now={Date.parse("2026-09-30T10:00:00Z")}
        onSelect={() => { document.body.dataset.opened = "task"; }} onTerminal={() => { document.body.dataset.opened = "terminal"; }} />
    </aside>
    <div style={{ display: "flex", flex: 1, minWidth: 0, height: "100vh" }}>
      <TerminalDeck snapshot={snapshot} task={task} cfo={false} shown connected focus={1} />
    </div>
  </main>);
}
window.reportSession = (phase) => flushSync(() => render(phase));
render(parameters.get("phase") || "working");
