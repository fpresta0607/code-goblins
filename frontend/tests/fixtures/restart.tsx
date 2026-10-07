import { createRoot } from "react-dom/client";
import { ComebackBanner } from "../../src/ComebackBanner";
import { StartAtLoginSetting } from "../../src/StartAtLoginSetting";
import { TaskCard } from "../../src/TaskCard";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

// A board after a restart, as the supervisor's snapshot says it: ?state=coming
// while goblins still wait to come back, and otherwise once all came back
// but one. Below the line, the card of the goblin that did not come back and
// of one that waits, and the CFO's Start at login, ?login=unavailable for a
// home with no desktop app.
const query = new URLSearchParams(location.search);
const isComing = query.get("state") === "coming";
const goblins = ["cg-tidy-home", "cg-site-hero", "cg-voice", "cg-dials", "cg-tickets", "cg-afk"];
const snapshot = parseSnapshot({
  healthy: true, instance: "fixture",
  comeback: {
    signed_in: "2026-10-06T22:00:00Z",
    cfo: { id: "cfo", state: "back" },
    goblins: goblins.map((id, index) => ({ id, state: id === "cg-site-hero" ? "stopped" : isComing && index > 1 ? "waiting" : "back", reason: id === "cg-site-hero" ? "Codex is not signed in" : "" })),
  },
  start_at_login: query.get("login") === "unavailable" ? { on: false, unavailable: "Start at login opens the desktop app, which this home does not hold" } : { on: true },
  tasks: [
    { id: "cg-site-hero", title: "Site download hero", project: "SIQstack-Website", harness: "codex", phase: "working", generation: "s1", verified: false,
      comeback: { id: "cg-site-hero", state: "stopped", reason: "Codex is not signed in" },
      action_error: "Did not come back after the restart: Codex is not signed in. The CFO was told." },
    { id: "cg-dials", title: "Subscription dials", project: "code-goblins", harness: "claude", phase: "working", generation: "s2", verified: false,
      comeback: { id: "cg-dials", state: "waiting" } },
  ],
});

function Page() {
  return <main style={{ display: "grid", alignContent: "start", gap: 20, minHeight: "100vh", boxSizing: "border-box", padding: 24 }}>
    <ComebackBanner comeback={snapshot.comeback} />
    <section className="board-column" aria-label="In progress" style={{ display: "grid", gap: 12, maxWidth: 360 }}>
      {snapshot.tasks.map((task) => <TaskCard key={task.id} task={task} snapshot={snapshot} selected={false} presentations={[]} now={Date.parse("2026-10-06T22:30:00Z")} onSelect={() => {}} onTerminal={() => {}} />)}
    </section>
    <section aria-label="CFO workspace" style={{ maxWidth: 480 }}>
      <StartAtLoginSetting setting={snapshot.start_at_login!} instance={snapshot.instance} />
    </section>
  </main>;
}

createRoot(document.getElementById("root")!).render(<Page />);
