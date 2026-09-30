import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { TaskCard } from "../../src/TaskCard";
import { TerminalDeck } from "../../src/TerminalDeck";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { reportSession: (phase: string, generation?: string, lifecycle?: { action: string; phase: string }) => void; showTask: (id: string) => void }
}

const root = createRoot(document.getElementById("root")!);
const parameters = new URLSearchParams(location.search);
const backend = parameters.get("backend") || "native";
// Three live Herdr goblins beside the ended one, for the deck's stream limit.
const crew = parameters.has("crew") ? ["alpha", "beta", "gamma"].map((id, index) => ({
  id, title: "Crew " + id, project: "code-goblins", backend: "herdr", harness: "codex", generation: "c" + (index + 1) + "-7f3a",
  session: "crew-" + id, phase: "working", verified: false, runtime: { state: "busy" },
})) : [];
let current = { phase: parameters.get("phase") || "working", generation: undefined as string | undefined, lifecycle: undefined as { action: string; phase: string } | undefined, shown: "" };
function render() {
  const { phase, generation, lifecycle, shown } = current;
  const isRetired = phase === "retired";
  const isMissing = parameters.has("missing");
  const snapshot = parseSnapshot({
    instance: "ended-proof", healthy: true,
    tasks: [{
      id: isRetired ? "finished:input-proof" : "input-proof", title: "Terminal fixes and handoff", project: "code-goblins",
      backend: isRetired ? "" : backend, harness: "claude", generation: generation ?? (isRetired || phase === "queued" ? "" : "s1-4b8e"), session: "session-proof",
      phase: isRetired ? "done" : phase, archived: isRetired, verified: false,
      runtime: { state: phase === "working" ? "busy" : phase === "done" ? "idle" : phase },
      activity: isMissing ? "" : "Terminal fixes verified. Pull request ready.", report: "done",
      last_report: isMissing ? "" : "Terminal fixes verified. Pull request ready.",
      at: isMissing ? "0001-01-01T00:00:00Z" : "2026-09-29T09:42:00Z", handoff: !isMissing,
      retired_at: isMissing || parameters.has("missing-time") ? "" : "2026-09-29T09:42:00Z",
      pr: "https://github.com/example/project/pull/218",
      lifecycle: lifecycle && { ...lifecycle, at: "2026-09-30T09:58:00Z", kept: [], stopped: [], problems: [], handoff_saved: false, validation_restarts: false },
    }, ...crew],
    sessions: [{ id: "session-proof", native_id: "b45f3bd2-2b4e-4ee8-8140-3537f8485a36", role: "goblin", task_id: "input-proof", generation: "s1-4b8e", phase: "working" }],
  });
  const task = snapshot.tasks.find((each) => each.id === shown) || snapshot.tasks[0];
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
window.reportSession = (phase, generation, lifecycle) => flushSync(() => { current = { ...current, phase, generation, lifecycle }; render(); });
window.showTask = (id) => flushSync(() => { current = { ...current, shown: id }; render(); });
render();
