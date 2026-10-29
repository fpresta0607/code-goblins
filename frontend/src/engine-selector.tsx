import { useId, useState } from "react";
import { message, request, useResource } from "./api";
import { object, string, type Task } from "./types";
import { parseEngineCatalog, type EngineSelection } from "./engine-catalog";
import { ConnectorMark } from "./ConnectorMark";
import { Icon } from "./Icon";
import { harnessMark } from "./connectors";
import { harnessName, taskColumn } from "./workflow";
import { goblinName } from "./task-words";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

export function EngineSelector({ task, instance }: { task: Task; instance: string }) {
  const label = useId();
  const isCompleted = taskColumn(task) === "Completed";
  const isQueued = task.phase === "queued", isPaused = task.phase === "paused";
  const catalog = useResource(isCompleted ? null : "/api/engines", parseEngineCatalog);
  const [draft, setDraft] = useState<EngineSelection | null>(null);
  const [saved, setSaved] = useState<{ choice: EngineSelection; revision: string; before: string } | null>(null);
  const [isSending, setSending] = useState(false);
  const [outcome, setOutcome] = useState("");
  const [feedback, showFeedback] = useClickFeedback();
  const receipt = saved && (task.queue_revision === saved.before || task.queue_revision === saved.revision) ? saved : null;
  const pending = task.pending_engine?.when === "resume" ? task.pending_engine : null;
  const current = pending || receipt?.choice || { harness: task.harness, model: task.model || (isQueued ? "" : "default"), effort: task.effort || (isQueued ? "" : "default") };
  const choice = draft || current;
  const harness = catalog.data?.find((item) => item.id === choice.harness);
  const model = harness?.models.find((item) => item.id === choice.model);
  const isAutomaticHarness = isQueued && !choice.harness, isAutomaticModel = isQueued && !choice.model, isAutomaticEffort = isQueued && !choice.effort;
  const unavailable = harness?.reason || (isAutomaticHarness ? "" : !harness ? "Harness " + harnessName(choice.harness) + " is unavailable" : isAutomaticModel ? "" : !model ? "Model " + choice.model + " is unavailable" : !isAutomaticEffort && choice.effort !== "default" && !model.efforts.includes(choice.effort) ? "Effort " + choice.effort + " is unavailable for " + model.name : "");
  const isDisabled = isSending || task.starting || task.switching || !isQueued && !isPaused && task.backend !== "native";
  // A working goblin restarts at once, as the CFO does: on its own engine
  // when nothing was changed, and switched onto the one shown otherwise.
  const send = async (when: "" | "now" | "restart" | "cancel") => {
    if (isSending) return;
    setSending(true); setOutcome(""); showFeedback("");
    try {
      const response = object(await request("/api/tasks/engine", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": instance }, body: JSON.stringify({ task: task.id, generation: task.generation, revision: receipt?.revision || task.queue_revision, ...choice, when }) }));
      if (isQueued || isPaused) {
        if (isQueued) setSaved({ choice, revision: string(response.revision), before: task.queue_revision });
        setOutcome(isQueued ? "Saved for Start" : "Saved for Resume");
      } else if (when === "cancel") setOutcome("Pending choice cancelled");
      setDraft(null);
    } catch (failure: unknown) { showFeedback(message(failure)); }
    finally { setSending(false); }
  };
  return <div className="engine-selector" role="group" aria-label="Task engine">
    {isCompleted ? task.harness || task.model ? <div className="engine-readonly"><ConnectorMark mark={harnessMark(task.harness)} label={harnessName(task.harness)} /><span>{harnessName(task.harness)}</span><span className="mono">{[task.model, task.effort].filter(Boolean).join(" ") || "Model not recorded"}</span></div> : <p className="muted">Engine not recorded</p> : <>
      {catalog.error ? <button className="icon-button raised" aria-label="Retry" onClick={catalog.reload}><Icon name="refresh" /></button> : !catalog.data ? <p className="loading" role="status">Reading engines…</p> : <>
        <div className="engine-fields">
          <label htmlFor={label + "-harness"}>Harness<span className="engine-harness"><ConnectorMark mark={harnessMark(choice.harness)} label={isAutomaticHarness ? "Automatic" : harnessName(choice.harness)} /><select id={label + "-harness"} aria-label="Harness" value={choice.harness} disabled={isDisabled} onChange={(event) => {
            const next = catalog.data?.find((item) => item.id === event.target.value), first = next?.models[0];
            setDraft(isQueued ? { ...choice, harness: event.target.value } : { harness: event.target.value, model: first?.id || "default", effort: first?.default_effort && first.efforts.includes(first.default_effort) ? first.default_effort : "default" }); setOutcome("");
          }}>{isQueued && <option value="">Automatic</option>}{!harness && !isAutomaticHarness && <option value={choice.harness} disabled>{harnessName(choice.harness)} (unavailable)</option>}{catalog.data.map((item) => <option key={item.id} value={item.id} disabled={!!item.reason}>{item.name}{item.reason ? " - " + item.reason : ""}</option>)}</select></span></label>
          <label htmlFor={label + "-model"}>Model<select id={label + "-model"} value={choice.model} disabled={isDisabled || !!harness?.reason || !harness && (!isQueued || !choice.model)} onChange={(event) => {
            const next = harness?.models.find((item) => item.id === event.target.value);
            setDraft({ ...choice, model: event.target.value, effort: isQueued ? choice.effort : next?.default_effort && next.efforts.includes(next.default_effort) ? next.default_effort : "default" }); setOutcome("");
          }}>{isQueued && <option value="">Automatic</option>}{!model && !isAutomaticModel && <option value={choice.model} disabled>{choice.model}{isAutomaticHarness ? "" : " (unavailable)"}</option>}{harness?.models.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
          <label htmlFor={label + "-effort"}>Effort<select id={label + "-effort"} value={choice.effort} disabled={isDisabled || !model && (!isQueued || !choice.effort)} onChange={(event) => { setDraft({ ...choice, effort: event.target.value }); setOutcome(""); }}><option value="default">Default</option>{isQueued && <option value="">Automatic</option>}{!isAutomaticEffort && choice.effort !== "default" && !model?.efforts.includes(choice.effort) && <option value={choice.effort} disabled>{choice.effort}{isAutomaticHarness || isAutomaticModel ? "" : " (unavailable)"}</option>}{model?.efforts.map((effort) => <option key={effort} value={effort}>{effort}</option>)}</select></label>
        </div>
        {unavailable && <p className="muted">{unavailable}</p>}
        {isQueued || isPaused ? draft && <button disabled={isDisabled || !!unavailable} onClick={() => void send("")}>{isQueued ? "Save" : "Save for Resume"}</button>
          : <button className="labelled-button cfo-restart-button" aria-label={"Restart " + goblinName(task)} disabled={isDisabled || !!draft && !!unavailable} onClick={() => void send(draft ? "now" : "restart")}><Icon name="refresh" />Restart</button>}
      </>}
      <ClickFeedback text={feedback} />
      {(isSending || task.switching || outcome) && <p role="status">{isSending ? "Saving..." : task.switching ? "Switching..." : outcome}</p>}
      {task.pending_engine?.when === "turn-end" && <p className="engine-pending">Pending: {task.pending_engine.model} {task.pending_engine.effort} <button disabled={isSending || task.switching} onClick={() => void send("cancel")}>Cancel pending</button></p>}
      {!isQueued && !isPaused && task.backend !== "native" && <p className="muted">Only a task in a native terminal can switch.</p>}
    </>}
  </div>;
}
