import { useState } from "react";
import { createRoot } from "react-dom/client";
import { FirstRun } from "../../src/FirstRun";
import "../../src/styles.css";

// The first-run page as the board's root shows it, over the page's own
// requests, which each test answers. What Start and the quiet link lead to
// is said in a line under the page.
function FirstRunFixture() {
  const [outcome, setOutcome] = useState("");
  return <main className="first-run-region" aria-label="First run">
    <FirstRun instance="fixture" onStarting={() => setOutcome("Starting the CEO")} onStarted={() => setOutcome("The CEO's terminal opens")} onBoard={() => setOutcome("The board opens without a CEO")} />
    <p role="status" aria-label="Outcome">{outcome}</p>
  </main>;
}

createRoot(document.getElementById("root")!).render(<FirstRunFixture />);
