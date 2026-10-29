import { useId, useState } from "react";
import { message, request, useResource } from "./api";
import { parseEngineCatalog, type EngineSelection } from "./engine-catalog";
import { ConnectorMark } from "./ConnectorMark";
import { Icon } from "./Icon";
import { harnessMark } from "./connectors";
import { harnessName } from "./workflow";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import type { Snapshot } from "./types";

// The CFO's own engine: the harness, model and effort it runs, saved for a
// safe turn boundary of the CFO rather than sent as a restart, so choosing one
// never interrupts a turn, a draft, a gate or any other goblin.
export function CfoEngineSelector({ snapshot }: { snapshot: Snapshot }) {
  const label = useId();
  const catalog = useResource("/api/engines", parseEngineCatalog);
  const received = snapshot.cfo_engine;
  const [draft, setDraft] = useState<EngineSelection | null>(null);
  const [isSaving, setSaving] = useState(false);
  const [feedback, showFeedback] = useClickFeedback();
  const choice = draft || received || { harness: snapshot.cfo_harness, model: "", effort: "" };
  const harness = catalog.data?.find((item) => item.id === choice.harness);
  const model = harness?.models.find((item) => item.id === choice.model);
  const unavailable = harness?.reason
    || (choice.harness && !harness ? "Harness " + harnessName(choice.harness) + " is unavailable" : "")
    || (choice.model && !model ? "Model " + choice.model + " is unavailable" : "")
    || (model && choice.effort && !model.efforts.includes(choice.effort) ? "Effort " + choice.effort + " is unavailable for " + model.name : "");
  const save = async () => {
    if (isSaving) return;
    setSaving(true); showFeedback("");
    try {
      await request("/api/cfo/engine", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify(choice) });
      setDraft(null);
      showFeedback("Saved");
    } catch (failure: unknown) { showFeedback(message(failure)); }
    finally { setSaving(false); }
  };
  return <div className="engine-selector" role="group" aria-label="CFO engine">
    {catalog.error ? <button className="icon-button raised" aria-label="Retry" onClick={catalog.reload}><Icon name="refresh" /></button> : !catalog.data ? <p className="loading" role="status">Reading engines…</p> : <>
      <div className="engine-fields">
        <label htmlFor={label + "-harness"}>Harness<span className="engine-harness"><ConnectorMark mark={harnessMark(choice.harness)} label={harnessName(choice.harness)} /><select id={label + "-harness"} value={choice.harness} disabled={isSaving} onChange={(event) => {
          setDraft({ harness: event.target.value, model: "", effort: "" });
        }}>{!harness && choice.harness && <option value={choice.harness} disabled>{harnessName(choice.harness)} (unavailable)</option>}{catalog.data.map((item) => <option key={item.id} value={item.id} disabled={!!item.reason}>{item.name}{item.reason ? " - " + item.reason : ""}</option>)}</select></span></label>
        <label htmlFor={label + "-model"}>Model<select id={label + "-model"} value={choice.model} disabled={isSaving || !harness || !!harness.reason} onChange={(event) => {
          const next = harness?.models.find((item) => item.id === event.target.value);
          setDraft({ ...choice, model: event.target.value, effort: next?.default_effort && next.efforts.includes(next.default_effort) ? next.default_effort : "" });
        }}><option value="" disabled>Choose model</option>{!model && choice.model && <option value={choice.model} disabled>{choice.model} (unavailable)</option>}{harness?.models.filter((item) => item.id !== "default").map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        <label htmlFor={label + "-effort"}>Effort<select id={label + "-effort"} value={choice.effort} disabled={isSaving || !model} onChange={(event) => { setDraft({ ...choice, effort: event.target.value }); }}><option value="" disabled>Choose effort</option>{choice.effort && !model?.efforts.includes(choice.effort) && <option value={choice.effort} disabled>{choice.effort} (unavailable)</option>}{model?.efforts.map((effort) => <option key={effort} value={effort}>{effort}</option>)}</select></label>
      </div>
      {unavailable && <p className="muted">{unavailable}</p>}
      <button className="labelled-button" disabled={isSaving || !draft || !!unavailable} onClick={() => void save()}><Icon name="refresh" />Save</button>
    </>}
    <ClickFeedback text={feedback} />
    {received?.pending && <p className="engine-pending">Pending: {received.model} {received.effort}{received.waiting && <span className="muted"> · {received.waiting}</span>}</p>}
    {received?.problem && <p className="muted">Not applied: {received.problem}</p>}
  </div>;
}
