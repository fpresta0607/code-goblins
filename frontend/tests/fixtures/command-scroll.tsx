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
});

function CommandScroll() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  return <main>
    <button onClick={() => setFocus({ key: "question:build-the-paused-divider", at: Date.now() })}>Open</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<CommandScroll />);
