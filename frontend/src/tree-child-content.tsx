import { memo } from "react";
import { BabyGoblin } from "./BabyGoblin";
import type { Baby } from "./fleet-tree";

export const TreeChildContent = memo(function TreeChildContent({ baby, name, runs, model, phase, state, when, memory }: {
  baby: Baby; name: string; runs: string; model: string; phase: string; state: string; when: string; memory: string;
}) {
  return <>
    <BabyGoblin baby={baby} hasTip={false} />
    <span className="tree-child-copy">
      <strong>{name}</strong>
      <span className="tree-child-runs">{runs}</span>
      {model && <span className="tree-child-model">{model}</span>}
      <span className={"state-chip phase-" + phase}><span className="status-dot" /><span className="state-chip-text">{state}{when && " " + when}</span></span>
    </span>
    {memory && <span className="tree-memory">{memory}</span>}
  </>;
});
