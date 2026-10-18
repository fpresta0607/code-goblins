import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Overlord's card of 2026-10-17: the CFO asks him to roll a token and
// gives its page as a value to copy, with another address in its words, and
// History holds a goblin's wait that named its page.
const NOW = Date.parse("2026-10-17T18:20:00Z");
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60000).toISOString();
const question = {
  id: "roll-github-token", identity: "c".repeat(64), task: "", status: "pending", created_at: minutesAgo(1), options: ["Done", "Later"], recommended: "",
  text: "Roll the GitHub token the fleet uses, then sign gh in again here with gh auth login.\n- Link: `https://github.com/settings/tokens`\n- Why: https://docs.github.com/en/authentication explains it. Never ftp://example.com or javascript:alert(1).",
};
const review = {
  id: "waiting-cg-store-picker-7", identity: "e".repeat(64), task: "cg-store-picker", image_count: 0, lavish: "", link: "", watched: false, answered_by: "", answered_in: "", window_closed_at: "", revising_since: "", document: null,
  title: "Waiting on you: Pick a store layout on the page (page https://example.com/session/f26e)", state: "answered", answer: "The grid", answer_id: "answer-7", delivered: true, reason: "Answered on its page.", created_at: minutesAgo(40), updated_at: minutesAgo(12),
};
const scrawlURL = "http://127.0.0.1:4387/session/private-review?mode=notes#answer";
const privateQuestion = { ...question, id: "private-scrawl", identity: "d".repeat(64), text: "Review the private Scrawl page\n- Page: " + scrawlURL + "\n- Address: `" + scrawlURL + "`" };
const privateReview = { ...review, id: "private-scrawl-history", identity: "f".repeat(64), title: "Private Scrawl feedback on " + scrawlURL };
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", revision: 1, actions: [],
  tasks: [{ id: "cg-store-picker", title: "cg-store-picker", goblin_name: "Pearl", goblin_title: "Shelf Arranger", phase: "working", generation: "s1", verified: false, reported_at: minutesAgo(3), merged: false }],
  questions: [question, privateQuestion], reviews: [review, privateReview],
});

createRoot(document.getElementById("root")!).render(<main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>);
