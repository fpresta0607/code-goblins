import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A goblin's sign-in, as fly-signin-precisiondocs-20261007 reached the
// Overlord on 2026-10-08, beside the CFO's question. window.cardStep is the
// supervisor's next snapshot: "running" once he pressed Run and its terminal
// runs on the card, "elevating" for an administrator's command Windows has
// not started yet, then how it ended: "complete" with exit 0, "failed" with
// exit 1, or "stopped" by him.
const goblin = { id: "fly-signin", title: "fly-signin", project: "precisiondocs", phase: "waiting", report: "waiting", waiting_on: "overlord", activity: "waiting on overlord: Sign in to Fly so I can deploy", generation: "s1", verified: false };
const run = { id: "run-fly-signin-3", identity: "g".repeat(64), task: goblin.id, title: "Sign in to Fly so I can deploy", shell: "powershell", command: "fly auth login", cwd: "C:\\dev\\code-goblins\\worktrees\\precisiondocs\\fly-signin", state: "ready", created_at: "2026-10-08T00:00:00Z", expires_at: "2026-10-09T00:00:00Z" };
const question = { id: "merge-train-20261008", identity: "c".repeat(64), task: "", status: "pending", created_at: "2026-10-08T00:01:00Z", text: "May I merge the release train now?", options: ["Merge it", "Hold it"], recommended: "Merge it" };
const base = { healthy: true, instance: "fixture", cfo_runs: true, tasks: [goblin], questions: [question] };
const running = { ...run, state: "running", ran_at: "2026-10-08T00:02:00Z", terminal: true };
const ended = (fields: Record<string, unknown>) => ({ ...running, terminal: false, finished_at: "2026-10-08T00:03:00Z", ...fields });
const snapshots = {
  ready: parseSnapshot({ ...base, revision: 1, runs: [run] }),
  running: parseSnapshot({ ...base, revision: 2, runs: [running] }),
  elevating: parseSnapshot({ ...base, revision: 2, runs: [{ ...running, admin: true, terminal: false }] }),
  complete: parseSnapshot({ ...base, revision: 3, runs: [ended({ state: "succeeded", exit_code: 0, output: "Opening https://fly.io/app/auth/cli\nsuccessfully logged in as overlord@example.com\n" })] }),
  failed: parseSnapshot({ ...base, revision: 3, runs: [ended({ state: "failed", exit_code: 1, output: "Opening https://fly.io/app/auth/cli\nError: the sign-in was cancelled in the browser\n" })] }),
  stopped: parseSnapshot({ ...base, revision: 3, runs: [ended({ state: "stopped", reason: "the Overlord stopped it", output: "Waiting for the sign-in in the browser\n" })] }),
};
const ignore = () => {};

declare global { interface Window { cardStep: (name: keyof typeof snapshots) => void } }

function Page() {
  const [shown, setShown] = useState<keyof typeof snapshots>("ready");
  useEffect(() => { window.cardStep = setShown; }, []);
  return <main data-step={shown} style={{ minHeight: "100vh", padding: 24 }}>
    <CommandCenter snapshot={snapshots[shown]} connected presentations={[]} focus={null} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
