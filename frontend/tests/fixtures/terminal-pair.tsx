import { useState } from "react";
import { createRoot } from "react-dom/client";
import { HostTerminal } from "../../src/HostTerminal";
import "../../src/styles.css";

// Two terminals open side by side, as the CFO's terminal stays open behind a
// run's terminal on its Command Center card. The button takes the second out
// of sight and brings it back, as switching terminals does.
function Fixture() {
  const [isSecondShown, setSecondShown] = useState(true);
  return <div style={{ display: "flex", gap: 8, height: "100vh", width: "100vw" }}>
    <div style={{ display: "flex", flex: 1, minWidth: 0 }}>
      <HostTerminal query="run=first" harness="" label="First terminal" instance="fixture" visible shown focus={1} />
    </div>
    <div style={{ display: isSecondShown ? "flex" : "none", flex: 1, minWidth: 0 }}>
      <HostTerminal query="run=second" harness="" label="Second terminal" instance="fixture" visible shown={isSecondShown} focus={0} />
    </div>
    <button style={{ position: "fixed", top: 0, left: 0, zIndex: 10 }} onClick={() => setSecondShown((isShown) => !isShown)}>Switch terminal</button>
  </div>;
}

createRoot(document.getElementById("root")!).render(<Fixture />);
