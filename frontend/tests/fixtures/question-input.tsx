import { createRoot } from "react-dom/client";
import { CommandCenter } from "../../src/CommandCenter";
import { parseSnapshot } from "../../src/types";
import "../../src/styles.css";

const OPTIONS = ["**Mark incident 37780310 a false positive in GitGuardian and keep history**", "Run `git status`", "Keep history"];
const BASE = { task: "", status: "pending", created_at: "2026-10-27T14:30:00Z" };
const SNAPSHOT = parseSnapshot({ healthy: true, instance: "fixture", revision: 1, questions: [
  { ...BASE, id: "incident", identity: "a".repeat(64), text: "What should happen to incident 37780310?", options: OPTIONS, recommended: OPTIONS[0] },
  { ...BASE, id: "written", identity: "b".repeat(64), text: "How should we handle the history?", options: [] },
] });

function QuestionInput() {
  return <main><CommandCenter snapshot={SNAPSHOT} connected presentations={[]} focus={null} onUnsent={() => {}} /></main>;
}

createRoot(document.getElementById("root")!).render(<QuestionInput />);
