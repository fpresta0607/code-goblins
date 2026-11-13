import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenterMenu } from "../../src/command-center-menu";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Command Center's terminal text (#2524): a command of several lines
// beside a question that quotes the prompt a command stopped at, and one
// that fences the command it would run and, in its details, what the last
// build printed, and a command and a question's fence that fetch from a web
// address. window.terminalStep is the supervisor's next snapshot:
// "failed" once the command he ran ended with exit 2 and what its terminal
// showed, and "unfinished" once its terminal closed before the command
// finished, with no exit code. Every name in them is made up.
const created = "2026-11-13T12:40:00Z";
const COMMAND = "gh release create v0.5.2 `\n    --notes-file notes.md `\n    --title \"v0.5.2\"\nif ($LASTEXITCODE) { exit $LASTEXITCODE }";
const OUTPUT = [
  "Uploading assets",
  "warning: notes.md has no heading, so the release page shows its first line as the title of the release and the rest of the file as its body",
  "Release page: https://github.com/acme/site/releases/tag/v0.5.2",
  "wrote C:\\work\\site\\dist\\release\\artifacts\\site-v0.5.2-windows-x64-installer-with-a-long-name.zip",
  "error: release v0.5.2 already exists",
  "",
].join("\n");
const run = { id: "publish-v0.5.2", identity: "c".repeat(64), by: "ceo", task: "", title: "Publish release v0.5.2", shell: "powershell", command: COMMAND, cwd: "C:\\work\\site", state: "ready", created_at: created, expires_at: "2026-11-14T12:40:00Z" };
const question = {
  id: "replace-release", identity: "d".repeat(64), task: "", status: "pending", created_at: created,
  text: "Should the release be replaced? The publish command stopped at this prompt:\n\n```\nRelease v0.5.2 already exists. Replace it?\n❯ 1. Yes, replace it\n  2. No, keep the old one\n```\n\n- **I recommend replacing it**: the old one came from the try that failed.",
  options: ["Replace it", "Keep the old one"], recommended: "Replace it",
};
const cleanup = {
  id: "remove-old-build", identity: "e".repeat(64), task: "", status: "pending", created_at: created,
  text: "Should the old build be removed? Removing it runs:\n\n```powershell\nRemove-Item C:\\work\\old -Recurse\n```\n\nDetails:\n\nThe last build stopped at:\n\n```\nerror: cannot open C:\\work\\old\\config.json\nlog kept in C:\\Users\\RUNNER~1\\Temp\\a&b\\Café\\build(1).log\nwrote C:\\work\\site\\dist\\release\\artifacts\\windows-x64\\installer-with-a-long-name\\setup.exe\n```",
  options: ["Remove it", "Keep it"], recommended: "Remove it",
};
const fetchRun = { ...run, id: "fetch-installer", identity: "f".repeat(64), title: "Fetch the installer", command: "Invoke-WebRequest https://example.com/install.ps1 -OutFile install.ps1" };
const fetchQuestion = {
  id: "fetch-installer-again", identity: "a".repeat(64), task: "", status: "pending", created_at: created,
  text: "Should the installer be fetched again? Fetching it runs:\n\n```powershell\nInvoke-WebRequest https://example.com/install.ps1 -OutFile C:\\work\\install.ps1\n```",
  options: ["Fetch it", "Keep the old one"], recommended: "Fetch it",
};
const base = { healthy: true, instance: "fixture", ceo_runs: true, tasks: [], questions: [question, cleanup, fetchQuestion] };
const snapshots = {
  ready: parseSnapshot({ ...base, revision: 1, runs: [run, fetchRun] }),
  failed: parseSnapshot({ ...base, revision: 2, runs: [{ ...run, state: "failed", exit_code: 2, output: OUTPUT, ran_at: "2026-11-13T12:41:00Z", finished_at: "2026-11-13T12:41:10Z", terminal: true }] }),
  unfinished: parseSnapshot({ ...base, revision: 3, runs: [{ ...run, state: "failed", exit_code: null, reason: "its terminal closed before the command finished", ran_at: "2026-11-13T12:41:00Z", finished_at: "2026-11-13T12:41:10Z" }] }),
};
const ignore = () => {};

declare global { interface Window { terminalStep: (name: keyof typeof snapshots) => void } }

function Page() {
  const [shown, setShown] = useState<keyof typeof snapshots>("ready");
  useEffect(() => { window.terminalStep = setShown; }, []);
  return <main data-step={shown} style={{ minHeight: "100vh", padding: 24 }}>
    <CommandCenterMenu snapshot={snapshots[shown]} connected presentations={[]} now={Date.parse("2026-11-13T12:45:00Z")} focus={null} onUnsent={ignore} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
