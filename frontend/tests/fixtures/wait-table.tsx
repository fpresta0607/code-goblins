import { useState } from "react";
import { createRoot } from "react-dom/client";
import { CommandCenter, type CommandFocus } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A goblin waits on the Overlord to add three DNS records, as
// cg-precisiondocs-mcp did on 2026-09-28, written the way cfo notify's help
// says: a lead sentence, a table of values in backticks, a closing line, and
// the dashboard link given with --link. A second goblin only names an address
// in its words, and a third opens its wait with the table itself.
const at = "2026-09-28T20:10:00Z";
const records = {
  id: "waiting-cg-precisiondocs-mcp-41", identity: "a".repeat(64), task: "cg-precisiondocs-mcp", state: "open", created_at: at, updated_at: at,
  link: "https://dash.cloudflare.com/precisiondocs/dns",
  title: "Waiting on you: Add these three DNS records in **Cloudflare** for precisiondocs.ai, then tell me\n"
    + "| Type | Name | Content | Proxy |\n| --- | --- | --- | --- |\n"
    + "| CNAME | `mcp` | `mcp-precisiondocs.fly.dev` | Off |\n"
    + "| TXT | `_acme-challenge.mcp` | `9fQe2kLx7Rm0aPz4Vb8Nw` | Off |\n"
    + "| AAAA | `mcp` | `2a09:8280:1::4f:2c1a` | Off |\n"
    + "So mcp.precisiondocs.ai serves the connector.",
};
const named = {
  id: "waiting-cg-board-polish-9", identity: "b".repeat(64), task: "cg-board-polish", state: "open", created_at: "2026-09-28T20:12:00Z", updated_at: "2026-09-28T20:12:00Z",
  title: "Waiting on you: check that https://mcp.precisiondocs.ai answers, then tell me",
};
const tableFirst = {
  id: "waiting-cg-mail-records-3", identity: "c".repeat(64), task: "cg-mail-records", state: "open", created_at: "2026-09-28T20:14:00Z", updated_at: "2026-09-28T20:14:00Z",
  title: "Waiting on you: | Type | Name |\n| --- | --- |\n| MX | `mail` |\nAdd this record, then tell me",
};
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", revision: 1, reviews: [records, named, tableFirst],
  tasks: [
    { id: "cg-precisiondocs-mcp", title: "Serve the PrecisionDocs connector on its own domain", phase: "waiting", generation: "1", verified: false },
    { id: "cg-board-polish", title: "Polish the board", phase: "waiting", generation: "1", verified: false },
    { id: "cg-mail-records", title: "Set up the mail records", phase: "waiting", generation: "1", verified: false },
  ],
});

function WaitTable() {
  const [focus, setFocus] = useState<CommandFocus | null>(null);
  return <main>
    <button onClick={() => setFocus({ key: "review:" + records.id, at: Date.now() })}>Open</button>
    <CommandCenter snapshot={snapshot} connected presentations={[]} focus={focus} onUnsent={() => {}} />
  </main>;
}

createRoot(document.getElementById("root")!).render(<WaitTable />);
