import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenterMenu } from "../../src/command-center-menu";
import type { CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  // answerUnconfirmed is the supervisor's next snapshot once he answered the
  // CFO's review on its page and the CFO never picked his answer up.
  interface Window { answerUnconfirmed: () => void }
}

// pd-agent-context waits on the Overlord with its audit on a Scrawl page, as
// it did on 2026-10-02 when its card said everything twice, and the CFO asks
// him to review a page of its own.
const at = "2026-10-02T12:00:00Z";
const audit = "http://127.0.0.1:4387/session/f26e";
const proof = "http://127.0.0.1:4387/session/a1b2";
const wait = { id: "waiting-pd-agent-context-7", identity: "a".repeat(64), task: "pd-agent-context", lavish: audit, lavish_page: "C:\\data\\audit\\index.html",
  title: "Waiting on you: Audit of what travels to his agent is ready on Scrawl, read-only, nothing built. (page " + audit + ")", state: "open", created_at: at, updated_at: at };
const review = { id: "proof-review-1", identity: "b".repeat(64), task: "", lavish: proof, title: "Do the proof screenshots read well?", state: "open", created_at: at, updated_at: at };
const fields = { healthy: true, instance: "fixture", tasks: [{ id: "pd-agent-context", title: "pd-agent-context", phase: "waiting", generation: "1", verified: false }] };
const snapshot = parseSnapshot({ ...fields, revision: 1, reviews: [wait, review] });
// What to do about the answer, with a page that says more and a javascript:
// string that stays words.
const advice = "Your answer was typed for the CEO, which has not picked it up. See https://docs.example.com/answers/unconfirmed for what to do, and never paste javascript:alert(1) into its box.";
const answeredAt = "2026-10-02T12:05:00Z";
const unconfirmed = parseSnapshot({ ...fields, revision: 2,
  reviews: [wait, { ...review, state: "answered", answer: "They read well", answer_id: "answer-proof", updated_at: answeredAt }],
  actions: [{ id: "answer-proof", kind: "review_answer", generation: review.identity, status: "uncertain", message: advice, advice, updated_at: answeredAt }] });

function WaitCardOnce() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const [shown, setShown] = useState(snapshot);
  window.answerUnconfirmed = () => setShown(unconfirmed);
  return <main>
    <button onClick={() => setFocus({ key: "review:waiting-pd-agent-context-7", at: Date.now() })}>Open the wait</button>
    <button onClick={() => setFocus({ key: "review:proof-review-1", at: Date.now() })}>Open the review</button>
    <CommandCenterMenu snapshot={shown} connected presentations={[]} now={Date.now()} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<WaitCardOnce />);
