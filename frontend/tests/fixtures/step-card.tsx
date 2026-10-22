import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { stepBoard?: { request: (id: string, changes: Record<string, unknown>) => void } }
}

// A goblin's browser work reached three steps only the Overlord can take: a
// sign-up that asks him to prove he is human, an account's email and API
// token to enter, and a sign-in that must happen in the goblin's own browser.
// stepBoard.request changes a card, as the supervisor's snapshot does after his
// press.
const at = "2026-10-14T23:40:00Z";
const cards: Record<string, unknown>[] = [
  {
    id: "cred-1111111111111111", generation: "a".repeat(32), identity: "a".repeat(64), by: "goblin", task: "shop-signup", project: "acme-shop",
    names: [], why: "Create the shop's test account", link: "https://shop.example.com/signup",
    steps: ["Prove you are a human", "Create the account with the shop's email", "Accept the terms"],
    state: "open", created_at: at, expires_at: "2026-10-15T23:40:00Z",
  },
  {
    id: "cred-2222222222222222", generation: "b".repeat(32), identity: "a".repeat(64), by: "goblin", task: "shop-signup", project: "acme-shop",
    names: ["SHOP_API_TOKEN"], plain: ["ACCOUNT_EMAIL"], labels: { ACCOUNT_EMAIL: "The account's email", SHOP_API_TOKEN: "The API token from Settings" },
    why: "Connect the tests to the new account", link: "https://shop.example.com/settings/api", steps: ["Create a token named fleet tests and copy it"],
    state: "open", created_at: "2026-10-14T23:41:00Z", expires_at: "2026-10-15T23:41:00Z",
  },
  {
    id: "cred-4444444444444444", generation: "d".repeat(32), identity: "a".repeat(64), by: "goblin", task: "shop-signup", project: "acme-ledger",
    names: [], plain: ["MADE_UP_ACCOUNT_REF"], labels: { MADE_UP_ACCOUNT_REF: "The account reference" },
    why: "Connect the tests to the new account", link: "https://shop.example.com/settings/api", steps: ["Copy the account reference"],
    state: "open", created_at: "2026-10-14T23:43:00Z", expires_at: "2026-10-15T23:43:00Z",
  },
  {
    id: "cred-3333333333333333", generation: "c".repeat(32), identity: "a".repeat(64), by: "goblin", task: "shop-signup", project: "acme-shop",
    names: [], why: "Sign in to the shop in my browser", link: "https://shop.example.com/login", window: true,
    steps: ["Sign in with your password or passkey", "Press Done once the dashboard shows"],
    state: "open", created_at: "2026-10-14T23:42:00Z", expires_at: "2026-10-15T23:42:00Z",
  },
];
const base = { healthy: true, instance: "fixture-token", revision: 1, tasks: [{ id: "shop-signup", title: "Sign the shop up for the test account", phase: "working", generation: "1", verified: false }], credentials: cards, runs: [] as Record<string, unknown>[] };

function StepCards() {
  const [state, setState] = useState(base);
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  window.stepBoard = {
    request: (id, changes) => setState((prior) => ({ ...prior, revision: prior.revision + 1, credentials: prior.credentials.map((card) => card.id === id ? { ...card, ...changes } : card) })),
  };
  return <main>
    {cards.map((card) => <button key={String(card.id)} onClick={() => setFocus({ key: "credential:" + String(card.id), at: Date.now() })}>{"Open " + String(card.id)}</button>)}
    <CommandCenter snapshot={parseSnapshot(state)} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<StepCards />);
