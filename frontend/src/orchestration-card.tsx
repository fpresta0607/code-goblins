import { memo } from "react";
import { Avatar } from "./Avatar";
import type { Persona } from "./workflow";

export const OrchestrationCard = memo(function OrchestrationCard({ title, status, phase, persona, project, relation, isPresenting }: {
  title: string; status: string; phase: string | undefined; persona: Persona; project: string; relation: string; isPresenting: boolean;
}) {
  return <>
    <Avatar persona={persona} />
    <span className="card-copy"><strong>{title}</strong>{isPresenting && <span className="browser-indicator">Browser active</span>}{project && <span className="project-label">{project}</span>}
      <span className={"plain-status phase-" + phase}><span className="status-dot" /><span className="card-status-text">{status}</span></span>
      {relation && <small>{relation}</small>}
    </span>
  </>;
});
