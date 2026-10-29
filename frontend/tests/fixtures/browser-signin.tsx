import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import { useItemState } from "../../src/use-item-state";
import "../../src/styles.css";

declare global {
  interface Window { signInFixture?: { items: (changes: Record<string, unknown>) => void; connection: (isConnected: boolean) => void } }
}

const SIGN_IN = {
  id: "signin-stripe", generation: "generation-one", login: "stripe-account", provider: "Stripe", account: "PrecisionDocs account",
  project: "PrecisionDocs-AI", task: "billing", state: "ready", reason: "", created_at: "2026-10-12T10:00:00Z", expires_at: "2026-10-13T10:00:00Z", closed_at: "",
};
const NEXT = { id: "next", task: "billing", title: "Review the billing page", state: "open", created_at: "2026-10-12T10:01:00Z" };

function SignInFixture() {
  const [received, setReceived] = useState(() => parseSnapshot({ healthy: true, instance: "fixture-token", revision: 1, browser_signins: [SIGN_IN], reviews: [NEXT] }));
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  const [isConnected, setConnected] = useState(true);
  const { snapshot } = useItemState(received);
  window.signInFixture = {
    items: (changes) => setReceived((prior) => parseSnapshot({ ...prior, revision: prior.revision + 1, browser_signins: [{ ...prior.browser_signins?.[0], ...changes }] })),
    connection: setConnected,
  };
  return <main>
    <button onClick={() => setFocus({ key: "browser-signin:signin-stripe", at: Date.now() })}>Open</button>
    {snapshot && <CommandCenter snapshot={snapshot} connected={isConnected} presentations={[]} focus={focus} onUnsent={() => {}} />}
  </main>;
}

createRoot(document.getElementById("root")!).render(<SignInFixture />);
