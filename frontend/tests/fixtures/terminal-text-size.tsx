import { createRoot } from "react-dom/client";
import { HostTerminal } from "../../src/HostTerminal";
import { TerminalTextSize } from "../../src/TerminalTextSize";
import "../../src/styles.css";

// A terminal's panel as the board lays it out: the compact header, with the
// text size's buttons at its end, above the terminal.
createRoot(document.getElementById("root")!).render(
  <aside className="context-pane" tabIndex={-1} aria-label="Goblin panel" style={{ height: 700, width: 1000 }}>
    <header className="panel-header compact"><span /><div className="panel-identity"><h2 id="panel-title">Goblin</h2></div><div className="panel-actions"><TerminalTextSize /></div></header>
    <HostTerminal query="task=size-proof&generation=proof" harness="" label="Goblin terminal" instance="fixture" visible shown focus={1} />
  </aside>,
);
