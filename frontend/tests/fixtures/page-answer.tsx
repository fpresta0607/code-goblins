import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { answerOnPage?: () => void }
}

// A goblin waits on the Overlord with a review page, and another goblin has an
// image review after it. Open shows the page's card; answerOnPage is the
// supervisor recording his answer on the page, as it does once its poll takes
// his feedback.
const page = "http://127.0.0.1:4387/session/f26e";
const at = "2026-09-30T23:20:00Z";
const wait = {
  id: "waiting-cg-board-theme-3584", identity: "a".repeat(64), task: "cg-board-theme", title: "Waiting on you: approve the mockups (page " + page + ")",
  lavish: page, lavish_page: "C:\\data\\cg-board-theme\\review\\review.html", state: "open", created_at: at, updated_at: at,
};
const base = {
  healthy: true, instance: "fixture", revision: 1,
  tasks: [
    { id: "cg-board-theme", title: "Restyle the board", phase: "waiting", generation: "1", verified: false },
    { id: "cg-board-polish", title: "Polish the board", phase: "working", generation: "1", verified: false },
  ],
  reviews: [wait, { id: "icons-cg-board-polish", identity: "b".repeat(64), task: "cg-board-polish", title: "Look at the new icons", state: "open", created_at: "2026-09-30T23:21:00Z", updated_at: "2026-09-30T23:21:00Z" }],
};
const answered = { ...base, revision: 2, reviews: [{ ...wait, state: "answered", answered_by: "overlord", answered_in: "page", reason: "You answered on its page; the CFO relays it to the goblin.", updated_at: "2026-09-30T23:29:38Z" }, base.reviews[1]] };

function PageAnswer() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  window.answerOnPage = () => setSnapshot(parseSnapshot(answered));
  return <main>
    <button onClick={() => setFocus({ key: "review:" + wait.id, at: Date.now() })}>Open</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<PageAnswer />);
