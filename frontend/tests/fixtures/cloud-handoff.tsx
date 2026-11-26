import { createRoot } from "react-dom/client";
import { CloudHandoffDetails } from "../../src/cloud-handoff-details";
import { cardFacts, TaskCard } from "../../src/TaskCard";
import { TaskControls } from "../../src/task-controls";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A working goblin, which Hand off sends to a cloud session, or with
// ?task=cloud one handed off, paused for the handoff with its packet in its
// pull request, which reads In the cloud and offers the prompt to copy, or
// with ?task=helper a working helper, which is not handed off.
const which = new URLSearchParams(location.search).get("task");
const isHandedOff = which === "cloud";
const PROMPT = "Repository o/r. Read the goblins handoff packet in the body of https://github.com/o/r/pull/209 and follow its rules: branch from the head it names, push only to your own branch and open your pull request into the branch it names.";
const goblin = isHandedOff
  ? { phase: "paused", lifecycle: { phase: "paused", action: "pause", at: "2026-11-25T19:00:00Z", kept: [], stopped: [], problems: [], handoff_saved: true, validation_kept: false, pause: { reason: "handoff", until: "", at: "2026-11-25T19:00:00Z" } }, cloud_handoff: { pull_request: "https://github.com/o/r/pull/209", prompt: PROMPT, cloud: [] } }
  : { phase: "working", runtime: { state: "busy" }, parent: which === "helper" ? "cg-lead" : "" };
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", ceo_runs: true, revision: 1, tasks: [{ id: "cg-parser", title: "Parse headers", project: "code-goblins", backend: "native", harness: "claude", generation: "s1", verified: false, ...goblin }] });
const task = snapshot.tasks[0];

createRoot(document.getElementById("root")!).render(<main style={{ display: "grid", gap: 16, maxWidth: 420, padding: 16 }}>
  <TaskCard task={task} {...cardFacts(task, snapshot, [])} selected now={Date.parse("2026-11-25T19:05:00Z")} onSelect={() => {}} />
  <div className="panel-content lifecycle-panel">
    <TaskControls task={task} snapshot={snapshot} />
    <CloudHandoffDetails task={task} />
  </div>
</main>);
