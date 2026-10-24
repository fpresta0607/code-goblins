import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// Kiki's IPRoyal refund, 2026-10-17, as each kind of card that can carry a
// message he sends himself: a step card whose step names the notes file, the
// CFO's question naming a folder with spaces in its name, and a command that
// runs in a folder. Each carries the email, so he copies it from the card. A
// wait on him names the notes file in its one line.
const at = "2026-10-17T22:00:00Z";
const goblin = { id: "pd-proxy-primary", title: "Move the PD proxy to IPRoyal", phase: "waiting", generation: "1", verified: false };
const NOTES = String.raw`C:\dev\code-goblins\data\pd-proxy-primary\iproyal-refund.md`;
const DESKTOP = String.raw`C:\Users\fpres\OneDrive - SIQstack\Desktop`;
const WORK = String.raw`C:\work\pd-proxy`;
const EMAIL = { to: "support@iproyal.com", subject: "Refund for order 4412", body: "Hello,\n\nPlease refund order 4412: the proxies never connected.\n\nThanks,\nNick" };
const FORM = { to: "https://iproyal.com/contact", subject: "", body: "Please refund order 4412.\nThe proxies never connected." };
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture-token", revision: 1, cfo_runs: true, tasks: [goblin],
  credentials: [{ id: "cred-5555555555555555", generation: "e".repeat(32), identity: "a".repeat(64), by: "goblin", task: goblin.id, project: "pd-proxy", names: [],
    why: "Send the IPRoyal refund email", steps: ["Read the refund notes in " + NOTES + ".", "Send the email below and press Done", "Then move the notes to `" + DESKTOP + "`"], mail: EMAIL, state: "open", created_at: at, expires_at: "2026-10-18T22:00:00Z" }],
  questions: [{ id: "q-proxy-notes", identity: "q".repeat(64), text: "Keep the proxy notes in `" + DESKTOP + "` once the refund is sent?", options: ["Keep them", "Delete them"], recommended: "Keep them", mail: FORM, status: "pending", created_at: at }],
  reviews: [{ id: "waiting-pd-proxy-primary-7", identity: "w".repeat(64), task: goblin.id, title: "Waiting on you: send the refund email in " + NOTES, state: "open", created_at: at, updated_at: at }],
  runs: [{ id: "run-pd-proxy-primary-9", identity: "r".repeat(64), task: goblin.id, title: "Sign in to IPRoyal to send the refund", shell: "powershell", command: "Start-Process https://iproyal.com/login", cwd: WORK, mail: EMAIL, state: "ready", created_at: at, expires_at: "2026-10-18T22:00:00Z" }],
});

function CardMailBoard() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const open = (key: string) => setFocus({ key, at: Date.now() });
  return <main>
    <button onClick={() => open("credential:cred-5555555555555555")}>Open the step card</button>
    <button onClick={() => open("question:q-proxy-notes")}>Open the question</button>
    <button onClick={() => open("run:run-pd-proxy-primary-9")}>Open the command</button>
    <button onClick={() => open("review:waiting-pd-proxy-primary-7")}>Open the wait</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<CardMailBoard />);
