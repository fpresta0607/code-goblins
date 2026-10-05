import { useId, useState } from "react";
import { message, request, useResource } from "./api";
import { object, string, type Task } from "./types";
import { parseEngineCatalog, type EngineSelection } from "./engine-catalog";
import { ConnectorMark } from "./ConnectorMark";
import { harnessMark } from "./connectors";
import { harnessName, taskColumn } from "./workflow";
import { EngineSwitchDialog } from "./engine-switch-dialog";
import { plainText } from "./task-words";
import { RawDetails } from "./raw-details";

export function EngineSelector({ task, instance }: { task: Task; instance: string }) {
  const label = useId();
  const isCompleted = taskColumn(task) === "Completed";
  const isQueued = task.phase === "queued", isPaused = task.phase === "paused";
  const catalog = useResource(isCompleted ? null : "/api/engines", parseEngineCatalog);
  const [draft, setDraft] = useState<EngineSelection | null>(null);
  const [saved, setSaved] = useState<{ choice: EngineSelection; revision: string; before: string } | null>(null);
  const [isSending, setSending] = useState(false), [isConfirming, setConfirming] = useState(false);
  const [error, setError] = useState(""), [outcome, setOutcome] = useState("");
  const receipt = saved && (task.queue_revision === saved.before || task.queue_revision === saved.revision) ? saved : null;
  const pending = task.pending_engine?.when === "resume" ? task.pending_engine : null;
  const current = pending || receipt?.choice || { harness: task.harness, model: task.model || "default", effort: task.effort || "default" };
  const choice = draft || current;
  const harness = catalog.data?.find((item) => item.id === choice.harness);
  const model = harness?.models.find((item) => item.id === choice.model);
  const unavailable = harness?.reason || (!harness ? "Harness " + harnessName(choice.harness) + " is unavailable" : !model ? "Model " + choice.model + " is unavailable" : choice.effort !== "default" && !model.efforts.includes(choice.effort) ? "Effort " + choice.effort + " is unavailable for " + model.name : "");
  const isDisabled = isSending || task.starting || task.switching || !isQueued && !isPaused && task.backend !== "native";
  const send = async (when: "" | "turn-end" | "now" | "cancel") => {
    if (isSending) return;
    setSending(true); setConfirming(false); setError(""); setOutcome("");
    try {
      const response = object(await request("/api/tasks/engine", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ task: task.id, generation: task.generation, revision: receipt?.revision || task.queue_revision, ...choice, when }) }));
      if (isQueued || isPaused) {
        if (isQueued) setSaved({ choice, revision: string(response.revision), before: task.queue_revision });
        setOutcome(isQueued ? "Saved for Start" : "Saved for Resume");
      } else setOutcome(when === "cancel" ? "Pending choice cancelled" : when === "turn-end" ? "Choice recorded for turn end" : "Switch requested");
      setDraft(null);
    } catch (failure: unknown) { setError(message(failure)); }
    finally { setSending(false); }
  };
  return <div className="engine-selector" role="group" aria-label="Task engine">
    {isCompleted ? task.harness || task.model ? <div className="engine-readonly"><ConnectorMark mark={harnessMark(task.harness)} label={harnessName(task.harness)} /><span>{harnessName(task.harness)}</span><span className="mono">{[task.model, task.effort].filter(Boolean).join(" ") || "Model not recorded"}</span></div> : <p className="muted">Engine not recorded</p> : <>
      {catalog.error ? <div role="alert"><p>The engines could not be read. <button onClick={catalog.reload}>Retry</button></p><RawDetails lines={[catalog.error]} /></div> : !catalog.data ? <p className="loading" role="status">Reading engines…</p> : <>
        <div className="engine-fields">
          <label htmlFor={label + "-harness"}>Harness<span className="engine-harness"><ConnectorMark mark={harnessMark(choice.harness)} label={harnessName(choice.harness)} /><select id={label + "-harness"} aria-label="Harness" value={choice.harness} disabled={isDisabled} onChange={(event) => {
            const next = catalog.data?.find((item) => item.id === event.target.value), first = next?.models[0];
            setDraft({ harness: event.target.value, model: first?.id || "default", effort: first?.default_effort && first.efforts.includes(first.default_effort) ? first.default_effort : "default" }); setOutcome("");
          }}>{!harness && <option value={choice.harness} disabled>{harnessName(choice.harness)} (unavailable)</option>}{catalog.data.map((item) => <option key={item.id} value={item.id} disabled={!!item.reason}>{item.name}{item.reason ? " - " + item.reason : ""}</option>)}</select></span></label>
          <label htmlFor={label + "-model"}>Model<select id={label + "-model"} value={choice.model} disabled={isDisabled || !!harness?.reason} data-tip={choice.model} onChange={(event) => {
            const next = harness?.models.find((item) => item.id === event.target.value);
            setDraft({ ...choice, model: event.target.value, effort: next?.default_effort && next.efforts.includes(next.default_effort) ? next.default_effort : "default" }); setOutcome("");
          }}>{!model && <option value={choice.model} disabled>{choice.model} (unavailable)</option>}{harness?.models.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
          <label htmlFor={label + "-effort"}>Effort<select id={label + "-effort"} value={choice.effort} disabled={isDisabled || !model} onChange={(event) => { setDraft({ ...choice, effort: event.target.value }); setOutcome(""); }}><option value="default">Default</option>{choice.effort !== "default" && !model?.efforts.includes(choice.effort) && <option value={choice.effort} disabled>{choice.effort} (unavailable)</option>}{model?.efforts.map((effort) => <option key={effort} value={effort}>{effort}</option>)}</select></label>
        </div>
        {unavailable && <p className="muted">{unavailable}</p>}
        {draft && <button disabled={isDisabled || !!unavailable} onClick={() => isQueued || isPaused ? void send("") : setConfirming(true)}>{isQueued ? "Save" : isPaused ? "Save for Resume" : "Apply"}</button>}
      </>}
      {(isSending || task.switching || outcome) && <p role="status">{isSending ? "Saving..." : task.switching ? "Switching..." : outcome}</p>}
      {task.pending_engine?.when === "turn-end" && <p className="engine-pending">Pending: {task.pending_engine.model} {task.pending_engine.effort} <button disabled={isSending || task.switching} onClick={() => void send("cancel")}>Cancel pending</button></p>}
      {error && <p role="alert">{plainText(error)}</p>}
      {!isQueued && !isPaused && task.backend !== "native" && <p className="muted">Only a task in a native terminal can switch.</p>}
      {isConfirming && <EngineSwitchDialog task={task} choice={choice} onSwitch={(when) => void send(when)} onClose={() => setConfirming(false)} />}
    </>}
  </div>;
}
