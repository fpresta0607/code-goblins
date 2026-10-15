import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The Command Center's History as the Overlord reads it at 05:00Z on
// 2026-10-15: long goblin waits withdrawn or answered, a question the CFO
// answered with its reason, a page he answered and a card he cleared.
const NOW = Date.parse("2026-10-15T05:00:00Z");
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60000).toISOString();
const review = (id: string, task: string, title: string, state: string, reason: string, closed: number, answer = "") => ({
  id, identity: "e".repeat(64), task, title, image_count: 0, lavish: "", link: "", watched: false, answered_by: "", answered_in: "", window_closed_at: "", revising_since: "", document: null,
  state, answer, answer_id: answer ? "answer-" + id : "", delivered: !!answer, reason, created_at: minutesAgo(closed + 30), updated_at: minutesAgo(closed),
});
const reviews = [
  review("waiting-cg-dns-records-4", "cg-dns-records", "Waiting on you: Add the DNS records in Cloudflare for the docs site, then tell me\n| Type | Name | Value |\n| --- | --- | --- |\n| CNAME | docs | cname.vercel-dns.com |\n| TXT | _vercel | vc-domain-verify=docs.example.com,4f2a |", "answered", "", 4, "Added both records, the TXT one took a minute to show"),
  review("waiting-cg-store-picker-7", "cg-store-picker", "Waiting on you: Pick a store layout on the page, the grid with the large cards or the list with prices on the right (page http://127.0.0.1:4387/session/f26e)", "answered", "Answered on its page.", 12, "The grid, with the prices under each card"),
  review("cg-theme-mockups", "cg-board-theme", "Three mockups of the new night theme for the board, with the lantern glow on waiting cards and the softer greens on finished ones", "cleared", "Cleared from the Command Center.", 40),
  review("waiting-cg-signin-flow-3", "cg-signin-flow", "Waiting on you: Sign in to the staging dashboard with your work account so I can record the sign-in flow end to end", "withdrawn", "cg-signin-flow reported again: done: PR https://github.com/example/board/pull/412", 95),
];
const question = {
  id: "notify-cg-verify-fast-12", identity: "a".repeat(64), task: "cg-verify-fast", generation: "s1", text: "May PR 2's gate start now, or should it wait for the running test step of PR 1 to end first?", options: ["Start PR 2's gate now", "Wait for the running test step to end"], recommended: "Wait for the running test step to end",
  created_at: minutesAgo(8), answered_at: minutesAgo(6), status: "succeeded", answer: "Wait for the running test step to end. The running test step ends in 4 minutes and two gates at once run the machine out of memory", answer_kind: "option", answered_option: "Wait for the running test step to end", answered_by: "cfo",
};
const task = (id: string, name: string, title: string) => ({ id, title: id, goblin_name: name, goblin_title: title, phase: "working", generation: "s1", verified: false, reported_at: minutesAgo(3), merged: false });
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", revision: 1, actions: [],
  tasks: [task("cg-dns-records", "Hank", "Record Keeper"), task("cg-store-picker", "Pearl", "Shelf Arranger"), task("cg-board-theme", "Lucy", "Pixel Polisher"), task("cg-signin-flow", "Otto", "Door Warden"), task("cg-verify-fast", "Bernie", "Gate Keeper")],
  questions: [question], reviews,
});

createRoot(document.getElementById("root")!).render(<main><CommandCenter snapshot={snapshot} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>);
