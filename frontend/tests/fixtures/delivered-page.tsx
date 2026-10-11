import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Command Center at 01:30Z on 2026-10-11 with the one item the CFO handed
// the Overlord, the picture of the board fix, after, as cfo deliver now makes
// it: a Scrawl page the supervisor watches, which shows the file.
const NOW = Date.parse("2026-10-11T01:30:00Z");
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60000).toISOString();
const delivered = {
  id: "board-card-text-fits-20261010", identity: "f".repeat(64), task: "", title: "The orchestration card, after the fit", state: "open", created_at: minutesAgo(3), updated_at: minutesAgo(3),
  lavish: "http://127.0.0.1:4387/session/after-card", lavish_page: "C:/state/reviews/after-card/page.html",
  document: { name: "after-card.webp", size: 22014, kind: "image/webp", download: false },
};
const snapshot = parseSnapshot({ healthy: true, instance: "fixture", revision: 1, actions: [], tasks: [], questions: [], reviews: [delivered] });

// The item opens as a click on its row opens it, once the board is up.
function DeliveredPage() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  useEffect(() => setFocus({ key: "review:" + delivered.id, at: 1 }), []);
  return <main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<DeliveredPage />);
