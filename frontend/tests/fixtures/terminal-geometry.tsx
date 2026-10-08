import { useState } from "react";
import { createRoot } from "react-dom/client";
import { HostTerminal } from "../../src/HostTerminal";
import "../../src/styles.css";

// The terminal fills the window, so resizing the window resizes its panel. The
// button over it takes the terminal out of sight and brings it back, as
// switching terminals does.
function Fixture() {
  const [shown, setShown] = useState(true);
  return <div style={{ display: "flex", height: "100vh", width: "100vw" }}>
    <HostTerminal query="task=geometry-proof&generation=proof" harness="" label="Goblin terminal" instance="fixture" visible shown={shown} focus={1} />
    <button style={{ position: "fixed", top: 0, left: 0, zIndex: 10 }} onClick={() => setShown((isShown) => !isShown)}>Switch terminal</button>
  </div>;
}

createRoot(document.getElementById("root")!).render(<Fixture />);
