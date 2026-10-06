import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { revise?: () => void; nextVersion?: () => void }
}

// cg-board-polish waits on the Overlord with its kanban mockups on a review
// page. revise is the supervisor taking his revision from the page (Send,
// without ending the review); nextVersion is the goblin's next version of the
// same page, which replaces the page in the same item.
const page = "http://127.0.0.1:4387/session/f26e";
const at = "2026-10-01T05:00:00Z";
const wait = {
  id: "waiting-cg-board-polish-41", identity: "a".repeat(64), task: "cg-board-polish", title: "Waiting on you: approve the kanban mockups (page " + page + ")",
  lavish: page, lavish_page: "C:\\dev\\code-goblins\\data\\cg-board-polish\\review-kanban\\index.html", state: "open", created_at: at, updated_at: at,
};
const base = { healthy: true, instance: "fixture", revision: 1, tasks: [{ id: "cg-board-polish", title: "Small board polish as the Overlord reports it", phase: "waiting", generation: "1", verified: false }], reviews: [wait] };
const revised = { ...base, revision: 2, reviews: [{ ...wait, revising_since: "2026-10-01T05:09:00Z", updated_at: "2026-10-01T05:09:00Z" }] };
const next = { ...base, revision: 3, reviews: [{ ...wait, title: "Waiting on you: approve the kanban mockups, now with the Paused divider (page " + page + ")", created_at: "2026-10-01T05:20:00Z", updated_at: "2026-10-01T05:20:00Z" }] };

function PageRevision() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  window.revise = () => setSnapshot(parseSnapshot(revised));
  window.nextVersion = () => setSnapshot(parseSnapshot(next));
  return <main>
    <button onClick={() => setFocus({ key: "review:" + wait.id, at: Date.now() })}>Open</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<PageRevision />);
