import { createRoot } from "react-dom/client";
import { HostTerminal } from "../../src/HostTerminal";
import { NativeTerminal } from "../../src/NativeTerminal";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

const task = parseSnapshot({ healthy: true, tasks: [{ id: "input-proof", generation: "proof", backend: "herdr", verified: false }] }).tasks[0];
createRoot(document.getElementById("root")!).render(
  <div style={{ display: "flex", height: 700, width: 1000 }} onKeyDown={(event) => {
    if (event.key === "Escape") document.body.dataset.escaped = "true";
  }}>
    {location.hash === "#herdr"
      ? <NativeTerminal task={task} instance="fixture" visible shown focus={1} />
      : <HostTerminal query="task=input-proof&generation=proof" label="Goblin terminal" instance="fixture" visible shown focus={1} />}
  </div>,
);
