import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { holdClosed, remembered } from "../../src/item-state";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window {
    // cardBack is the supervisor's next snapshot once a card he brought back
    // from History waits on him again, under a new generation.
    cardBack?: (id: string) => void;
  }
}

const NOW = Date.parse("2026-10-15T13:45:00Z");
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60000).toISOString();
const later = new Date(NOW + 20 * 3600000).toISOString();
const question = (id: string, task: string, text: string, closed: number, fields: Record<string, unknown>) => ({
  id, identity: "a".repeat(64), task, generation: "s1", text, options: ["Yes", "No"], recommended: "Yes",
  created_at: minutesAgo(closed + 5), answered_at: minutesAgo(closed), status: "succeeded", answer: "Yes", answer_kind: "option", answered_option: "Yes", ...fields,
});
const card = (id: string, why: string, state: string, closed: number, fields: Record<string, unknown> = {}) => ({
  id, generation: id.slice(-1).repeat(32), identity: "c".repeat(64), by: "cfo", task: "push-settings", project: "sample-app",
  names: [], why, link: "https://example.com/settings", steps: ["Tick the box", "Press Save"],
  state, reason: state === "answered" ? "The box was already ticked" : "", created_at: minutesAgo(closed + 3), closed_at: minutesAgo(closed), expires_at: later, ...fields,
});
const questions = [
  question("notify-push-settings-4", "push-settings", "May I file the push key card now?", 1, { answered_by: "cfo" }),
  question("pp-merge-126", "", "Merge the release pull request?", 9, { answered_by: "overlord", answer_id: "board-merge" }),
  question("notify-cg-board-theme-3", "cg-board-theme", "Which accent should the board use?", 30, { answered_by: "cfo", answered_away: true }),
  question("notify-cg-signin-flow-2", "cg-signin-flow", "Should the sign-in page keep its old link?", 40, { status: "superseded", answer: "", answer_kind: "", answered_option: "", message: "Superseded. The asker was replaced." }),
];
const cards = [
  card("cred-1111111111111111", "Tick the App Group for the share extension", "done", 2),
  card("cred-2222222222222222", "Move the sign-in screen out of test mode", "dismissed", 3),
  card("cred-3333333333333333", "Turn on time sensitive notifications", "answered", 4),
  card("cred-4444444444444444", "The push key for the gateways", "saved", 5, { names: ["APNS_PRIVATE_KEY"], saved: ["APNS_PRIVATE_KEY"], link: "https://developer.example.com/account/keys", steps: ["download the .p8 key"] }),
  card("cred-6666666666666666", "Where the deploys report", "saved", 7, { by: "goblin", plain: ["DEPLOY_CHANNEL"], labels: { DEPLOY_CHANNEL: "Deploy channel" }, received: ["DEPLOY_CHANNEL"], link: "", steps: ["Pick the channel the team reads"] }),
  card("cred-5555555555555555", "Accept the new developer terms", "dismissed", 6, { expires_at: minutesAgo(1) }),
];
const reviews = [{
  id: "waiting-cg-dns-records-4", identity: "e".repeat(64), task: "cg-dns-records", title: "Waiting on you: Add the DNS records for the docs site", image_count: 0, lavish: "", link: "", watched: false, answered_by: "", answered_in: "", window_closed_at: "", revising_since: "", document: null,
  state: "answered", answer: "Added both records", answer_id: "answer-dns", delivered: true, reason: "", created_at: minutesAgo(50), updated_at: minutesAgo(20),
}];
const runs = [{ id: "publish-v0.5.2", identity: "c".repeat(64), by: "cfo", task: "", title: "Publish release v0.5.2", shell: "powershell", command: "gh release create v0.5.2", cwd: "C:\\example\\board", state: "succeeded", exit_code: 0, created_at: minutesAgo(60), ran_at: minutesAgo(26), finished_at: minutesAgo(25), expires_at: later }];
const task = (id: string, name: string, title: string) => ({ id, title: id, goblin_name: name, goblin_title: title, phase: "working", generation: "s1", verified: false, reported_at: minutesAgo(3), merged: false });
const base = {
  healthy: true, instance: "fixture-token", revision: 1, at: new Date(NOW).toISOString(), actions: [],
  tasks: [task("push-settings", "Hank", "Key Keeper"), task("cg-board-theme", "Lucy", "Pixel Polisher"), task("cg-signin-flow", "Otto", "Door Warden"), task("cg-dns-records", "Pearl", "Record Keeper")],
  questions, credentials: cards as Record<string, unknown>[], reviews, runs,
  browser_signins: [{ id: "signin-example", provider: "Example", project: "sample-app", account: "person@example.com", state: "signed_in", created_at: minutesAgo(55), closed_at: minutesAgo(45) }],
};
const closed = remembered(new Map(), parseSnapshot(base));

function HistorySame() {
  const [state, setState] = useState(base);
  window.cardBack = (id) => setState((prior) => ({
    ...prior, revision: prior.revision + 1,
    credentials: prior.credentials.map((candidate) => candidate.id === id ? { ...candidate, state: "open", closed_at: "", reason: "", generation: "9".repeat(32) } : candidate),
  }));
  return <main><CommandCenter snapshot={holdClosed(parseSnapshot(state), closed, new Map())} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<HistorySame />);
