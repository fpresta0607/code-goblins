import { createRoot } from "react-dom/client";
import { TerminalDeck } from "../../src/TerminalDeck";
import { NativeTerminal } from "../../src/NativeTerminal";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

const parameters = new URLSearchParams(location.search);
const isCfo = parameters.get("role") === "cfo";
const board = createRoot(document.getElementById("root")!);
// A hook update reports the harness of a session already running in the
// terminal; the ended CFO session before it shares that terminal.
const render = (harness: string) => {
  const snapshot = parseSnapshot({ healthy: true, instance: "fixture", cfo_terminal: "cfo-proof", cfo_runs: true,
    sessions: [
      { id: "old-cfo-session", native_id: "0b7d4f3e-5a61-4c2b-9e8d-1f2a3b4c5d6e", host_id: "cfo-proof", role: "cfo", harness: harness === "claude" ? "codex" : "claude", phase: "ended", updated_at: "2026-09-30T10:00:00Z" },
      { id: "cfo-session", native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "cfo-proof", role: "cfo", harness, updated_at: "2026-09-30T11:00:00Z" },
    ],
    tasks: [{ id: "input-proof", generation: "proof", backend: location.hash === "#herdr" ? "herdr" : "native", harness, verified: false }],
  });
  const task = snapshot.tasks[0];
  board.render(
    <div style={{ display: "flex", height: 700, width: 1000 }} onKeyDown={(event) => {
      if (event.key === "Escape") document.body.dataset.escaped = "true";
    }}>
      {location.hash === "#herdr"
        ? <NativeTerminal task={task} instance="fixture" visible shown focus={1} />
        : <TerminalDeck snapshot={snapshot} task={isCfo ? undefined : task} cfo={isCfo} shown connected focus={1} />}
    </div>,
  );
};
Object.assign(window, { reportHarness: render });
render(parameters.get("harness") || "");
