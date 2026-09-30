import { useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Lineage } from "../../src/Lineage";
import { Orchestration } from "../../src/Orchestration";
import { parseSnapshot } from "../../src/types";
import { useActivity } from "../../src/useActivity";
import { watchEffects } from "./effect-watch";
import "../../src/styles.css";

// A CFO with one goblin under it. Report makes the goblin report something
// new: a status line (report), a message the CFO sent it (message) or its
// native creation (created), in Orchestration or, with compact-, in the
// narrow screen's nested list. Report and Watch start watching the effects
// afresh.
const mode = location.hash.slice(1);
const session = (id: string, role: string, parent: string, task: string) => ({ id, native_id: id, harness: "codex", role, task_id: task, generation: "1", parent, relation: parent ? "Spawned" : "", phase: "working" });
const base = {
  healthy: true, instance: "fixture",
  sessions: [session("cfo", "cfo", "", ""), session("goblin", "goblin", "cfo", "build")],
  tasks: [{ id: "build", title: "Build the parser", project: "code-goblins", phase: "working", generation: "1", session: "goblin", verified: false, activity: "working: step 0" }],
};

function PulseEnd() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  const effects = useActivity(snapshot, true);
  const reports = useRef(0);
  const watch = () => {
    window.effectWatch?.stop();
    window.effectWatch = watchEffects(document.getElementById("effects")!);
  };
  const report = () => {
    const count = ++reports.current;
    watch();
    const kind = mode.replace("compact-", "");
    setSnapshot(parseSnapshot(kind === "report"
      ? { ...base, revision: count, tasks: [{ ...base.tasks[0], activity: "working: step " + count }] }
      : { ...base, revision: count, activity: [{ id: "event-" + count, kind, task_id: "build", generation: "1", source: "cfo", target: "goblin", state: "accepted", at: new Date().toISOString() }] }));
  };
  return <main>
    <button onClick={report}>Report</button>
    <button onClick={watch}>Watch</button>
    <div id="effects" style={{ height: 900, display: "flex", flexDirection: "column" }}>
      {mode.startsWith("compact-")
        ? <Lineage snapshot={snapshot} project="" selected={null} effects={effects} presentations={[]} onSelect={() => {}} />
        : <Orchestration snapshot={snapshot} selected="" connected effects={effects} presentations={[]} onSelect={() => {}} />}
    </div>
  </main>;
}

createRoot(document.getElementById("root")!).render(<PulseEnd />);
