import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { TerminalDeck } from "../../src/TerminalDeck";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";
import { SteppedIn } from "./stepped-in";

const parameters = new URLSearchParams(location.search);
const isCeo = parameters.get("role") === "ceo";
const isHerdr = location.hash === "#herdr";
const board = createRoot(document.getElementById("root")!);
// A hook update reports the harness of a session already running in the
// terminal; the ended CEO session before it shares that terminal. A CEO in
// Herdr has no native terminal.
const render = (harness: string) => {
  const snapshot = parseSnapshot({ healthy: true, instance: "fixture", ceo_terminal: isHerdr ? "" : "ceo-proof", ceo_runs: true, ceo_harness: harness,
    sessions: [
      { id: "old-ceo-session", native_id: "0b7d4f3e-5a61-4c2b-9e8d-1f2a3b4c5d6e", host_id: "ceo-proof", role: "ceo", harness: harness === "claude" ? "codex" : "claude", phase: "ended", updated_at: "2026-09-30T10:00:00Z" },
      { id: "ceo-session", native_id: "9c4e2a71-8b3d-4f6a-a5c2-7d1e0f9b8a34", host_id: "ceo-proof", role: "ceo", harness, updated_at: "2026-09-30T11:00:00Z" },
    ],
    tasks: [{ id: "input-proof", generation: "proof", backend: isHerdr ? "herdr" : "native", harness, verified: false }],
  });
  const task = snapshot.tasks[0];
  board.render(
    <div style={{ display: "flex", height: 700, width: 1000 }} onKeyDown={(event) => {
      if (event.key === "Escape") document.body.dataset.escaped = "true";
    }}>
      <SteppedIn><TerminalDeck snapshot={snapshot} task={isCeo ? undefined : task} ceo={isCeo} shown connected focus={1} /></SteppedIn>
    </div>,
  );
};
// The update commits before the next key, as a hook update long before a
// keypress does.
Object.assign(window, { reportHarness: (harness: string) => flushSync(() => render(harness)) });
render(parameters.get("harness") || "");
