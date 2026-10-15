import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A goblin has one of each Command Center card waiting on the Overlord, as on
// 2026-10-15 when he could neither skip nor answer a command in his own words:
// a command to run, a wait on him, a question and a credential card.
const at = "2026-10-15T01:50:00Z";
const goblin = { id: "shop-signup", title: "Sign the shop up for the test account", phase: "waiting", generation: "1", verified: false };
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture-token", revision: 1, cfo_runs: true, tasks: [goblin],
  runs: [{ id: "run-shop-signup-7", identity: "r".repeat(64), task: goblin.id, title: "Sign in to GitHub so I can push", shell: "powershell", command: "gh auth login --web", cwd: "C:\\work\\shop", state: "ready", created_at: at, expires_at: "2026-10-16T01:50:00Z" }],
  reviews: [{ id: "waiting-shop-signup-8", identity: "w".repeat(64), task: goblin.id, title: "Waiting on you: Confirm the shop's email from the message the sign-up sent", link: "https://mail.example.com/inbox", state: "open", created_at: at, updated_at: at }],
  questions: [{ id: "q-shop-plan", identity: "q".repeat(64), text: "Which plan should the test account use?", options: ["Free", "Pro"], recommended: "Free", status: "pending", created_at: at }],
  credentials: [{ id: "cred-4444444444444444", generation: "c".repeat(32), identity: "a".repeat(64), by: "goblin", task: goblin.id, project: "acme-shop", names: ["FLEET_GITHUB_TOKEN"], why: "Push the shop's branches", link: "https://github.com/settings/tokens", state: "open", created_at: at, expires_at: "2026-10-16T01:50:00Z" }],
});

function SkipOrOther() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const open = (key: string) => setFocus({ key, at: Date.now() });
  return <main>
    <button onClick={() => open("run:run-shop-signup-7")}>Open the command</button>
    <button onClick={() => open("review:waiting-shop-signup-8")}>Open the wait</button>
    <button onClick={() => open("question:q-shop-plan")}>Open the question</button>
    <button onClick={() => open("credential:cred-4444444444444444")}>Open the credential</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<SkipOrOther />);
