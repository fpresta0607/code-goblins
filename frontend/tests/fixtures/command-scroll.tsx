import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// Two questions the CFO has for the Overlord, each long enough that its card
// scrolls in the Command Center. Open opens the first, as a notification or
// the counter does.
const details = Array.from({ length: 40 }, (_, n) => "- Detail " + (n + 1) + " of what changes and why it matters to the board.").join("\n");
const question = (id: string, created_at: string, text: string) => ({ id, identity: "i-" + id, task: "", created_at, status: "pending", options: ["Build as drawn", "Change it first"], recommended: "Build as drawn", text: text + "\n" + details });
const card = new URLSearchParams(location.search).get("card") || "question:build-the-paused-divider";
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", revision: 1,
  tasks: [
    { id: "cg-board-polish", title: "Polish the board", phase: "blocked", generation: "1", verified: false },
    { id: "cg-board-theme", title: "Restyle the board", phase: "blocked", generation: "1", verified: false },
  ],
  attention: ["cg-board-polish", "cg-board-theme"],
  questions: [
    question("build-the-paused-divider", "2026-09-30T23:40:00Z", "May I build the Paused divider as drawn?"),
    question("restyle-the-header", "2026-09-30T23:41:00Z", "May I restyle the header as drawn?"),
  ],
  runs: [{ id: "long-command", identity: "run-1", title: "Run the board checks", command: details, state: "ready", shell: "powershell", created_at: "2026-09-30T23:42:00Z" }],
  credentials: [{ id: "long-credential", identity: "credential-1", generation: "1", project: "fixture", by: "cfo", names: [], plain: ["ACCOUNT_NAME"], labels: { ACCOUNT_NAME: "Account name" }, why: "Choose the test account", steps: details.split("\n"), state: "open", created_at: "2026-09-30T23:43:00Z" }],
  reviews: [{ id: "long-review", identity: "review-1", title: "Review the board changes\n" + details, state: "open", created_at: "2026-09-30T23:44:00Z" }],
});

function CommandScroll() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const [isMounted, setMounted] = useState(true);
  const shown = new URLSearchParams(location.search).has("single") ? { ...snapshot, questions: snapshot.questions?.slice(0, 1), runs: [], credentials: [], reviews: [] } : snapshot;
  return <main style={{ paddingTop: 600, minHeight: 2400 }}>
    <button onClick={() => setFocus({ key: card, at: Date.now() })}>Open</button>
    <button onClick={() => setMounted(false)}>Remove Command Center</button>
    {isMounted && <CommandCenter snapshot={shown} connected presentations={[]} focus={focus} onUnsent={() => {}} />}
  </main>;
}

createRoot(document.getElementById("root")!).render(<CommandScroll />);
