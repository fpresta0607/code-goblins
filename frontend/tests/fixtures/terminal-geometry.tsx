import { createRoot } from "react-dom/client";
import { HostTerminal } from "../../src/HostTerminal";
import "../../src/styles.css";

// The terminal fills the window, so resizing the window resizes its panel.
createRoot(document.getElementById("root")!).render(
  <div style={{ display: "flex", height: "100vh", width: "100vw" }}>
    <HostTerminal query="task=geometry-proof&generation=proof" harness="" label="Goblin terminal" instance="fixture" visible shown focus={1} />
  </div>,
);
