import { Icon } from "./Icon";
import type { PlanStatus, PlanStep } from "./session-messages";
import "./plan-card.css";

const STATUS_WORDS: Record<PlanStatus, string> = { pending: "To do", in_progress: "In progress", completed: "Done" };

// The checklist a call wrote, Claude Code's todo list or Codex's plan, as
// the Claude desktop app draws one in the conversation: each step with the
// mark of its state, the step in progress in full ink and the steps done
// quiet and struck through, with how many are done at its head. Nothing in
// it moves.
export function PlanCard({ steps }: { steps: PlanStep[] }) {
  const done = steps.filter((step) => step.status === "completed").length;
  return <section className="plan-card" aria-label="Plan">
    <p className="plan-card-head"><Icon name="task" /><span className="plan-card-title">Plan</span><span className="plan-card-count">{done} of {steps.length} done</span></p>
    <ol className="plan-steps">
      {steps.map((step, at) => <li key={at} className={"plan-step " + step.status}>
        <span className="plan-mark" aria-hidden="true">{step.status === "completed" && <Icon name="check" />}</span>
        <span className="plan-text">{step.text}</span>
        <span className="sr-only">{STATUS_WORDS[step.status]}</span>
      </li>)}
    </ol>
  </section>;
}
