import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { ReleaseBanner } from "../../src/release-banner";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A board on v0.4.2 when v0.5.0 is published: the supervisor's release check
// made the Update item, and the slim banner points to it. window.updateStep
// is the supervisor's next snapshot: "running" once the Overlord pressed
// Update, "updated" once the new build serves, "rolledBack" when it did not
// serve, with the board's next item for the same release beside it, and
// "source" for a board built from a clone.
const offer = { from: "v0.4.2", to: "v0.5.0", page: "https://github.com/fpresta0607/code-goblins/releases/tag/v0.5.0", published: "2026-10-06T14:02:00Z",
  notes: ["The installer sorts out every machine by itself", "AFK mode holds questions for you", "Goblins that stall wake the CFO"], signing: "unsigned", publisher: "", sum: "3f9a6c0e1d2b4a5f6e7d8c9b0a1f2e3d4c5b6a7f8e9d0c1b2a3f4e5d6c5bc21e" };
const run = { id: "update-v0.5.0-1", identity: "u".repeat(64), title: "Update Code Goblins from v0.4.2 to v0.5.0", shell: "powershell", command: "goblins update --to v0.5.0 --run update-v0.5.0-1", cwd: "C:\\Users\\you\\AppData\\Local\\CodeGoblins", state: "ready", created_at: "2026-10-06T14:05:00Z", expires_at: "2026-10-07T14:05:00Z", update: offer };
const release = { installed: "v0.4.2", tag: "v0.5.0", page: offer.page, published: offer.published, source: false };
const base = { healthy: true, instance: "fixture", cfo_runs: true, tasks: [], build: "old-build", release };
const snapshots = {
  ready: parseSnapshot({ ...base, revision: 1, runs: [run] }),
  running: parseSnapshot({ ...base, revision: 2, runs: [{ ...run, state: "running", ran_at: "2026-10-06T14:06:00Z" }] }),
  updated: parseSnapshot({ ...base, revision: 3, release: null, runs: [{ ...run, state: "succeeded", exit_code: 0, ran_at: "2026-10-06T14:06:00Z", finished_at: "2026-10-06T14:07:00Z",
    output: "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n[3/4] Install Code Goblins v0.5.0\n[4/4] Bring the home up to date\nUpdated: Code Goblins v0.5.0 runs.\n" }] }),
  rolledBack: parseSnapshot({ ...base, revision: 4, runs: [
    { ...run, state: "failed", exit_code: 3, ran_at: "2026-10-06T14:06:00Z", finished_at: "2026-10-06T14:08:00Z",
      output: "[1/4] Download Code Goblins v0.5.0\n[2/4] Check the download\n[3/4] Install Code Goblins v0.5.0\nRolled back: Code Goblins v0.4.2 serves again, and v0.5.0 was not installed; what it printed above says why.\n" },
    { ...run, id: "update-v0.5.0-2", created_at: "2026-10-06T14:08:30Z" }] }),
  source: parseSnapshot({ ...base, revision: 5, release: { ...release, installed: "main-8c10c45b", source: true }, runs: [] }),
};
const ignore = () => {};

declare global { interface Window { updateStep: (name: keyof typeof snapshots) => void } }

function Page() {
  const [shown, setShown] = useState<keyof typeof snapshots>("ready");
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  useEffect(() => { window.updateStep = setShown; }, []);
  return <main data-step={shown} style={{ minHeight: "100vh", padding: 24, display: "grid", alignContent: "start", gap: 12 }}>
    <ReleaseBanner snapshot={snapshots[shown]} onOpen={(key) => setFocus({ key, at: Date.now() })} />
    <CommandCenter snapshot={snapshots[shown]} connected presentations={[]} focus={focus} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
