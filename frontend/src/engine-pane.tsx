import { useEffect, useRef, type KeyboardEvent } from "react";
import { useResource } from "./api";
import { parseEngineCatalog, type EngineModel } from "./engine-catalog";
import { effortsInOrder, effortTone, type EngineSetting } from "./engine-choices";
import { HarnessMark } from "./harness-mark";
import { Icon } from "./Icon";
import { shortModel } from "./model-name";
import "./engine-pane.css";

// isModel says whether a catalog model is the one a session names, by its id
// or by the name the box shows for that id.
const isModel = (listed: EngineModel, named: string) => !!named && (listed.id === named || shortModel(listed.id) === shortModel(named));

// EnginePane is Modern Chat's own pane for a session's model or effort (the
// Overlord, 2026-10-27: "The modern chat should have its own panels to adjust
// the settings. It shouldn't rely on any terminal views or UI"). It lists
// what the session's harness really offers, read from its own catalog
// (GET /api/engines) only while the pane is open: its models, or the efforts
// of the model it runs, each effort in its level's colour. The one it runs is
// checked; a pick goes to onPick, and Escape or a press outside closes it.
// The keyboard moves through the choices with the arrows.
export function EnginePane({ setting, harness, model, effort, onPick, onClose }: {
  setting: EngineSetting; harness: string; model: string; effort: string;
  onPick: (value: string, chosen?: EngineModel) => void; onClose: () => void;
}) {
  const pane = useRef<HTMLDivElement>(null);
  const catalog = useResource("/api/engines", parseEngineCatalog);
  const offered = catalog.data?.find((item) => item.id === harness);
  const running = offered?.models.find((listed) => isModel(listed, model));
  const efforts = effortsInOrder(running?.efforts.length ? running.efforts : [...new Set(offered?.models.flatMap((listed) => listed.efforts) ?? [])]);
  const choices = setting === "model"
    ? (offered?.models ?? []).map((listed) => ({ value: listed.id, label: shortModel(listed.name) || listed.name, detail: listed.id === listed.name ? "" : listed.id, isChosen: isModel(listed, model), tone: "", chosen: listed }))
    : efforts.map((level) => ({ value: level, label: level, detail: "", isChosen: level === effort, tone: effortTone(level), chosen: undefined }));
  const isReady = choices.length > 0;
  useEffect(() => {
    if (!isReady) return;
    const items = Array.from(pane.current?.querySelectorAll<HTMLElement>("[role=menuitemradio]") ?? []);
    (items.find((item) => item.getAttribute("aria-checked") === "true") ?? items[0])?.focus();
  }, [isReady]);
  useEffect(() => {
    const away = (event: PointerEvent) => { if (!pane.current?.contains(event.target as Node)) onClose(); };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [onClose]);
  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); onClose(); return; }
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    const items = Array.from(pane.current?.querySelectorAll<HTMLElement>("[role=menuitemradio]") ?? []);
    const at = items.indexOf(document.activeElement as HTMLElement);
    items[(at + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length]?.focus();
  };
  const title = setting === "model" ? "Model" : "Effort";
  return <div ref={pane} className="popup-surface engine-pane" role="menu" aria-label={title} onKeyDown={keys}>
    <p className="engine-pane-title"><HarnessMark harness={harness} />{title}</p>
    {catalog.error ? <p className="engine-pane-note" role="alert">The session's {setting === "model" ? "models" : "efforts"} could not be read.</p>
      : !catalog.data ? <p className="engine-pane-note" role="status">Reading the session's {setting === "model" ? "models" : "efforts"}…</p>
      : offered?.reason ? <p className="engine-pane-note" role="status">{offered.reason}</p>
      : !isReady ? <p className="engine-pane-note" role="status">{setting === "model" ? "This harness lists no models." : "This model takes no effort setting."}</p>
      : choices.map((choice) => <button key={choice.value} type="button" role="menuitemradio" aria-checked={choice.isChosen} className="engine-choice"
        onClick={() => onPick(choice.value, choice.chosen)}>
        <span className={"engine-choice-label" + (choice.tone ? " effort-level " + choice.tone : "")}>{choice.label}</span>
        {choice.detail && <span className="engine-choice-detail">{choice.detail}</span>}
        {choice.isChosen && <Icon name="check" />}
      </button>)}
  </div>;
}
