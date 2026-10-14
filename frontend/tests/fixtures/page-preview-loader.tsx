import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A goblin's review page waits on the Overlord, and its card shows the page
// scaled down in a frame. Open opens the card, as its alert does.
const page = "http://127.0.0.1:4387/session/loader-page";
const at = "2026-10-14T22:40:00Z";
const wait = {
  id: "waiting-cg-loader-1", identity: "a".repeat(64), task: "cg-loader", lavish: page, lavish_page: "C:/data/cg-loader/review.html",
  title: "Waiting on you: the plan is on the page, reply ship it or say what to change (page " + page + ")", state: "open", created_at: at, updated_at: at,
};
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", revision: 1,
  tasks: [{ id: "cg-loader", title: "Show a loader", phase: "waiting", generation: "1", verified: false }],
  reviews: [wait], questions: [],
});

function PagePreviewLoader() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  return <main>
    <button onClick={() => setFocus({ key: "review:" + wait.id, at: Date.now() })}>Open</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<PagePreviewLoader />);
