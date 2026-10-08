import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The CFO's own command, as publish-v0.5.2 reached the Overlord on
// 2026-10-07, beside a question the CFO also has for him. window.runEnds is the
// supervisor's next snapshot: "running" once he pressed Run and its terminal
// runs on the card, then how the command ended: "finished" with exit 0, "errorLine" whose output ends in an
// error line but exits 0, "failed" with exit 2, or "withdrawn" by the CFO.
const created = "2026-10-07T17:55:00Z";
const run = { id: "publish-v0.5.2", identity: "c".repeat(64), by: "cfo", task: "", title: "Publish release v0.5.2", shell: "powershell", command: "gh release create v0.5.2 --notes-file notes.md", cwd: "C:\\dev\\code-goblins", state: "ready", created_at: created, expires_at: "2026-10-08T17:55:00Z" };
const question = { id: "merge-train-20261007", identity: "c".repeat(64), task: "", status: "pending", created_at: "2026-10-07T17:56:00Z", text: "May I merge the release train now?", options: ["Merge it", "Hold it"], recommended: "Merge it" };
const base = { healthy: true, instance: "fixture", cfo_runs: true, tasks: [], questions: [question] };
const running = { ...run, state: "running", ran_at: "2026-10-07T17:58:00Z", terminal: true };
const ended = (fields: Record<string, unknown>) => ({ ...running, finished_at: "2026-10-07T17:58:10Z", ...fields });
const snapshots = {
  ready: parseSnapshot({ ...base, revision: 1, runs: [run] }),
  running: parseSnapshot({ ...base, revision: 2, runs: [running] }),
  finished: parseSnapshot({ ...base, revision: 3, runs: [ended({ state: "succeeded", exit_code: 0, output: "Uploading assets\nPublished v0.5.2\n" })] }),
  errorLine: parseSnapshot({ ...base, revision: 3, runs: [ended({ state: "succeeded", exit_code: 0, output: "Uploading assets\nERROR: the release notes could not be attached\n" })] }),
  failed: parseSnapshot({ ...base, revision: 3, runs: [ended({ state: "failed", exit_code: 2, output: "Uploading assets\ngh: release v0.5.2 already exists\n" })] }),
  withdrawn: parseSnapshot({ ...base, revision: 3, runs: [{ ...run, state: "withdrawn", reason: "the release was published by hand" }] }),
};
const ignore = () => {};

declare global { interface Window { runEnds: (name: keyof typeof snapshots) => void } }

function Page() {
  const [shown, setShown] = useState<keyof typeof snapshots>("ready");
  useEffect(() => { window.runEnds = setShown; }, []);
  return <main data-step={shown} style={{ minHeight: "100vh", padding: 24 }}>
    <CommandCenter snapshot={snapshots[shown]} connected presentations={[]} focus={null} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
