import { useId, useState } from "react";
import { message, request, useResource } from "./api";
import { parseEngineCatalog, type EngineSelection } from "./engine-catalog";
import type { Snapshot } from "./types";

export function CfoEngineSelector({ snapshot }: { snapshot: Snapshot }) {
  const label = useId();
  const catalog = useResource("/api/engines", parseEngineCatalog);
  const [draft, setDraft] = useState<EngineSelection | null>(null);
  const [saved, setSaved] = useState<{ choice: EngineSelection; before: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState("");
  const received = snapshot.cfo_engine;
  const before = received ? [received.harness, received.model, received.effort].join("|") : "";
  const receipt = saved?.before === before ? saved.choice : null;
  const initial = receipt || received || { harness: snapshot.cfo_harness, model: "", effort: "" };
  const choice = draft || initial;
  const harness = catalog.data?.find((item) => item.id === choice.harness);
  const model = harness?.models.find((item) => item.id === choice.model);
  const valid = !!harness && !harness.reason && !!model && model.id !== "default" && model.efforts.includes(choice.effort);
  const save = async () => {
    setSaving(true); setProblem("");
    try {
      await request("/api/cfo/engine", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ harness: choice.harness, model: choice.model, effort: choice.effort }) });
      setSaved({ choice: { harness: choice.harness, model: choice.model, effort: choice.effort }, before });
      setDraft(null);
    } catch (error: unknown) { setProblem(message(error)); }
    finally { setSaving(false); }
  };
  return <div className="engine-selector" role="group" aria-label="CFO engine">
    {catalog.error ? <button onClick={catalog.reload}>Retry engines</button> : !catalog.data ? <p className="loading" role="status">Reading engines...</p> : <>
      <div className="engine-fields">
        <label htmlFor={label + "-harness"}>Harness<select id={label + "-harness"} value={choice.harness} disabled={saving} onChange={(event) => setDraft({ harness: event.target.value, model: "", effort: "" })}>
          <option value="" disabled>Choose harness</option>{!harness && choice.harness && <option value={choice.harness} disabled>{choice.harness} (unavailable)</option>}{catalog.data.map((item) => <option key={item.id} value={item.id} disabled={!!item.reason}>{item.name}{item.reason ? " - " + item.reason : ""}</option>)}
        </select></label>
        <label htmlFor={label + "-model"}>Model<select id={label + "-model"} value={choice.model} disabled={saving || !harness || !!harness.reason} onChange={(event) => {
          const next = harness?.models.find((item) => item.id === event.target.value);
          setDraft({ ...choice, model: event.target.value, effort: next?.default_effort && next.efforts.includes(next.default_effort) ? next.default_effort : "" });
        }}><option value="" disabled>Choose model</option>{!model && choice.model && <option value={choice.model} disabled>{choice.model} (unavailable)</option>}{harness?.models.filter((item) => item.id !== "default").map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        <label htmlFor={label + "-effort"}>Effort<select id={label + "-effort"} value={choice.effort} disabled={saving || !model} onChange={(event) => setDraft({ ...choice, effort: event.target.value })}>
          <option value="" disabled>Choose effort</option>{choice.effort && !model?.efforts.includes(choice.effort) && <option value={choice.effort} disabled>{choice.effort} (unavailable)</option>}{model?.efforts.map((effort) => <option key={effort} value={effort}>{effort}</option>)}
        </select></label>
      </div>
      {draft && choice.harness !== snapshot.cfo_harness && <p className="muted">Changing harness starts a new conversation.</p>}
      <button disabled={!draft || !valid || saving} onClick={() => void save()}>Save for the next safe turn</button>
    </>}
    {(problem || snapshot.cfo_engine?.problem) && <p role="alert">{problem || snapshot.cfo_engine?.problem}</p>}
    {snapshot.cfo_engine && !snapshot.cfo_engine.problem && <p role="status">{snapshot.cfo_engine.pending ? snapshot.cfo_engine.waiting || "Checking when the CFO can safely restart." : "Selected for the CFO's next start."}</p>}
  </div>;
}
