import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

declare global {
  interface Window { board?: { request: (id: string, changes: Record<string, unknown>) => void; runs: (runs: Record<string, unknown>[]) => void } }
}

// A goblin asks for two Stripe credentials for precisiondocs, one of which the
// scope already holds, and the CFO asks for a database URL. board.request
// changes a request and board.runs sets the board's runs, as the supervisor's
// snapshot does after a save or while a request's terminal is open.
const at = "2026-10-01T03:00:00Z";
const requests: Record<string, unknown>[] = [
  {
    id: "cred-0123456789abcdef", generation: "f".repeat(32), identity: "a".repeat(64), by: "goblin", task: "add-billing",
    project: "precisiondocs", repository: "C:\\dev\\precisiondocs", names: ["STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"],
    why: "Charge test cards in the billing tests", link: "https://dashboard.stripe.com/test/apikeys",
    existing: ["STRIPE_WEBHOOK_SECRET"], services: { STRIPE_SECRET_KEY: ["stripe"], STRIPE_WEBHOOK_SECRET: ["stripe"] },
    hints: [{ name: "STRIPE_SECRET_KEY", prefixes: ["sk_", "rk_"], warn: [{ prefix: "sk_live_", say: "A live secret key can do anything on the account; use a restricted rk_live_ key." }] }],
    state: "open", created_at: at, expires_at: "2026-10-02T03:00:00Z",
  },
  {
    id: "cred-fedcba9876543210", generation: "e".repeat(32), identity: "b".repeat(64), by: "cfo", task: "",
    project: "precisiondocs", repository: "C:\\dev\\precisiondocs", names: ["DATABASE_URL"], why: "Local database for docker compose",
    state: "open", created_at: "2026-10-01T03:05:00Z", expires_at: "2026-10-02T03:05:00Z",
  },
];
const base = { healthy: true, instance: "fixture-token", revision: 1, tasks: [{ id: "add-billing", title: "Add Stripe billing", phase: "working", generation: "1", verified: false }], credentials: requests, runs: [] as Record<string, unknown>[] };

function CredentialCards() {
  const [state, setState] = useState(base);
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  window.board = {
    request: (id, changes) => setState((prior) => ({ ...prior, revision: prior.revision + 1, credentials: prior.credentials.map((request) => request.id === id ? { ...request, ...changes } : request) })),
    runs: (runs) => setState((prior) => ({ ...prior, revision: prior.revision + 1, runs })),
  };
  return <main>
    <button onClick={() => setFocus({ key: "credential:cred-0123456789abcdef", at: Date.now() })}>Open</button>
    <CommandCenter snapshot={parseSnapshot(state)} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<CredentialCards />);
