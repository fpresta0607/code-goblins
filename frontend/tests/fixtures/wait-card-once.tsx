import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// pd-agent-context waits on the Overlord with its audit on a Scrawl page, as
// it did on 2026-10-02 when its card said everything twice, and the CFO asks
// him to review a page of its own.
const at = "2026-10-02T12:00:00Z";
const audit = "http://127.0.0.1:4387/session/f26e";
const proof = "http://127.0.0.1:4387/session/a1b2";
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", revision: 1,
  tasks: [{ id: "pd-agent-context", title: "pd-agent-context", phase: "waiting", generation: "1", verified: false }],
  reviews: [
    { id: "waiting-pd-agent-context-7", identity: "a".repeat(64), task: "pd-agent-context", lavish: audit, lavish_page: "C:\\data\\audit\\index.html",
      title: "Waiting on you: Audit of what travels to his agent is ready on Scrawl, read-only, nothing built. (page " + audit + ")", state: "open", created_at: at, updated_at: at },
    { id: "proof-review-1", identity: "b".repeat(64), task: "", lavish: proof, title: "Do the proof screenshots read well?", state: "open", created_at: at, updated_at: at },
  ],
});

function WaitCardOnce() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  return <main>
    <button onClick={() => setFocus({ key: "review:waiting-pd-agent-context-7", at: Date.now() })}>Open the wait</button>
    <button onClick={() => setFocus({ key: "review:proof-review-1", at: Date.now() })}>Open the review</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<WaitCardOnce />);
