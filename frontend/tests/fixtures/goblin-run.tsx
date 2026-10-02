import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A goblin waiting on the Overlord with a command for him to run, as
// cg-goblins-quickstart did on 2026-10-02, when its command sat inside a
// paragraph of its waiting card. The board shows it as a run card named for
// the goblin. window.runStep is the supervisor's next snapshot: "running" once
// he pressed Run and its window is open, "finished" once the command ended.
const goblin = { id: "cg-goblins-quickstart", title: "cg-goblins-quickstart", project: "code-goblins", phase: "waiting", report: "waiting", waiting_on: "overlord", activity: "waiting on overlord: Sign in to GitHub so I can push the branch (runs sign-in.ps1)", generation: "s1", verified: false };
const run = { id: "run-cg-goblins-quickstart-12", identity: "g".repeat(64), task: goblin.id, interactive: true, title: "Sign in to GitHub so I can push the branch", shell: "powershell", command: "gh auth login --web", cwd: "C:\\dev\\code-goblins\\.worktrees\\gb-cg-goblins-quickstart", state: "ready", created_at: "2026-10-02T01:20:00Z", expires_at: "2026-10-03T01:20:00Z" };
const base = { healthy: true, instance: "fixture", cfo_runs: true, tasks: [goblin] };
const snapshots = {
  ready: parseSnapshot({ ...base, revision: 1, runs: [run] }),
  running: parseSnapshot({ ...base, revision: 2, runs: [{ ...run, state: "running", ran_at: "2026-10-02T01:21:00Z" }] }),
  finished: parseSnapshot({ ...base, revision: 3, runs: [{ ...run, state: "succeeded", exit_code: 0, ran_at: "2026-10-02T01:21:00Z", finished_at: "2026-10-02T01:22:00Z" }] }),
};
const ignore = () => {};

declare global { interface Window { runStep: (name: keyof typeof snapshots) => void } }

function Page() {
  const [shown, setShown] = useState<keyof typeof snapshots>("ready");
  useEffect(() => { window.runStep = setShown; }, []);
  return <main data-step={shown} style={{ minHeight: "100vh", padding: 24 }}>
    <CommandCenter snapshot={snapshots[shown]} connected presentations={[]} focus={null} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
