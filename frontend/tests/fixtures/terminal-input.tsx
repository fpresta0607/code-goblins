import { createRoot } from "react-dom/client";
import { TerminalDeck } from "../../src/TerminalDeck";
import { NativeTerminal } from "../../src/NativeTerminal";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

const parameters = new URLSearchParams(location.search);
const harness = parameters.get("harness") || "";
const isCfo = parameters.get("role") === "cfo";
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", cfo_terminal: "cfo-proof", cfo_runs: true,
  sessions: [
    { id: "old-cfo-session", native_id: "old-cfo-proof", role: "cfo", harness: harness === "claude" ? "codex" : "claude", phase: "ended" },
    { id: "cfo-session", native_id: "cfo-proof", role: "cfo", harness },
  ],
  tasks: [{ id: "input-proof", generation: "proof", backend: location.hash === "#herdr" ? "herdr" : "native", harness, verified: false }],
});
const task = snapshot.tasks[0];
createRoot(document.getElementById("root")!).render(
  <div style={{ display: "flex", height: 700, width: 1000 }} onKeyDown={(event) => {
    if (event.key === "Escape") document.body.dataset.escaped = "true";
  }}>
    {location.hash === "#herdr"
      ? <NativeTerminal task={task} instance="fixture" visible shown focus={1} />
      : <TerminalDeck snapshot={snapshot} task={isCfo ? undefined : task} cfo={isCfo} shown connected focus={1} />}
  </div>,
);
