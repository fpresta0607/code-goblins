import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { closeWindow?: () => void }
}

// cg-board-polish waits on the Overlord with a review page and asks about it
// as a question too, as it did on 2026-09-30. Open opens the question, as its
// alert does; closeWindow is the supervisor seeing the page's window close.
const page = "http://127.0.0.1:4387/session/ec2ef7d06dddccbb";
const at = "2026-09-30T23:42:53Z";
const wait = {
  id: "waiting-cg-board-polish-3584", identity: "a".repeat(64), task: "cg-board-polish", lavish: page, lavish_page: "C:\\data\\review-kanban\\index.html",
  title: "Waiting on you: item 4 mockup on the page; reply build or say what to change (page " + page + ")", state: "open", created_at: at, updated_at: at, question: "notify-cg-board-polish-3585",
};
const asked = {
  id: "notify-cg-board-polish-3585", identity: "a".repeat(64), task: "cg-board-polish", generation: "1", seq: 3585, status: "pending", created_at: "2026-09-30T23:43:02Z", page: wait.id,
  text: "May I build item 4's Paused divider and layout switch as drawn?\n- Inside In progress, after the working cards, a thin divider labelled Paused with its count.",
  options: ["Build as drawn", "Change the divider first"], recommended: "Build as drawn",
};
const base = {
  healthy: true, instance: "fixture", revision: 1,
  tasks: [{ id: "cg-board-polish", title: "Polish the board", phase: "waiting", generation: "1", verified: false }],
  reviews: [wait], questions: [asked],
};

function PageQuestion() {
  const [snapshot, setSnapshot] = useState(() => parseSnapshot(base));
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  window.closeWindow = () => setSnapshot(parseSnapshot({ ...base, revision: 2, reviews: [{ ...wait, window_closed_at: "2026-09-30T23:47:18Z" }] }));
  return <main>
    <button onClick={() => setFocus({ key: "question:" + asked.id, at: Date.now() })}>Open</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<PageQuestion />);
