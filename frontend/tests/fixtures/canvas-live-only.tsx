import { createRoot } from "react-dom/client";
import { Lineage } from "../../src/Lineage";
import { Orchestration } from "../../src/Orchestration";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// The canvas the Overlord showed on 2026-10-17, in small. Wanda and Kip work
// under the CFO, Kip's helper Pip works for him and Wendell waits on CI.
// Beside them the board holds what read Turn finished, Working and Session
// ended there: the sessions of goblins retired the day before, whose tasks the
// board's history no longer names, an ended session, Wanda's earlier
// generation, a sub-agent with no terminal of its own under Wendell, Oona and
// Murray, paused, and a queued brief. With ?list the page draws the lineage
// list the board shows at phone width instead.
const session = (id: string, task: string, phase: string, generation = "g1") => ({ id: "claude/" + id, native_id: id, harness: "claude", role: "goblin", task_id: task, generation, phase });
const goblin = (id: string, name: string, title: string, phase: string, more: object = {}) => ({ id, title: id, goblin_name: name, goblin_title: title, project: "code-goblins", phase, generation: "g1", verified: false, ...more });
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture", cfo_runs: true, cfo_session: "claude/cfo",
  sessions: [
    session("model-routing", "cg-model-routing", "settled"),
    session("lavish-server-cwd", "lavish-server-cwd", "settled"),
    session("dictation-cold-start", "cg-dictation-cold-start", "active"),
    session("goblins-under-cfo", "cg-goblins-under-cfo", "ended"),
    session("api-ace-earlier", "pd-mcp-only", "active", "g0"),
    session("api-ace", "pd-mcp-only", "active"),
    session("chart-captain", "ha-spend-tab", "active"),
    session("chart-helper", "ha-spend-tab-h1", "active"),
    session("gate-whisperer", "nm-ci-step-ends-green", "settled"),
    { id: "claude/general-purpose", native_id: "general-purpose", harness: "claude", role: "subagent", task_id: "nm-ci-step-ends-green", generation: "g1", parent: "claude/gate-whisperer", relation: "delegated", agent_type: "general-purpose", phase: "active" },
    session("website-wizard", "itascaunity-site", "settled"),
  ],
  tasks: [
    goblin("pd-mcp-only", "Wanda", "API Ace", "working", { session: "claude/api-ace" }),
    goblin("ha-spend-tab", "Kip", "Chart Captain", "working", { session: "claude/chart-captain" }),
    goblin("ha-spend-tab-h1", "Pip", "Chart Helper", "working", { session: "claude/chart-helper", parent: "ha-spend-tab" }),
    goblin("nm-ci-step-ends-green", "Wendell", "Gate Whisperer", "waiting", { session: "claude/gate-whisperer", waiting_on: "ci" }),
    goblin("itascaunity-site", "Oona", "Website Wizard", "paused", { session: "claude/website-wizard" }),
    goblin("pd-agent-api-v2-later", "Murray", "API Artisan", "paused"),
    goblin("cg-next-brief", "Nell", "Brief Keeper", "queued", { generation: "" }),
  ],
});
const now = Date.parse("2026-10-17T17:40:00Z");
const isList = new URLSearchParams(location.search).has("list");

createRoot(document.getElementById("root")!).render(<main style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
  {isList
    ? <Lineage onOpenCfo={() => {}} snapshot={snapshot} project="" selected={null} effects={[]} presentations={[]} now={now} onSelect={() => {}} />
    : <Orchestration onOpenCfo={() => {}} snapshot={snapshot} selected="" connected effects={[]} presentations={[]} now={now} onSelect={() => {}} onChild={() => {}} />}
</main>);
