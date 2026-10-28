import { useEffect, useRef, type KeyboardEvent, type RefObject } from "react";
import { reportToCfo, useResource } from "./api";
import { parseEngineCatalog, type EngineModel } from "./engine-catalog";
import { effortsInOrder, effortTone, type EngineSetting } from "./engine-choices";
import { HarnessMark } from "./harness-mark";
import { Icon } from "./Icon";
import { shortModel } from "./model-name";
import "./engine-pane.css";

// EnginePane is Modern Chat's own pane for a session's model or effort (the
// Overlord, 2026-10-27: "The modern chat should have its own panels to adjust
// the settings. It shouldn't rely on any terminal views or UI"). It lists
// what the session's harness really offers, read from its own catalog
// (GET /api/engines) only while the pane is open: its models, or the efforts
// of the model it runs, each effort in its level's colour. The one it runs is
// checked: the model whose id the session names, or, when no id matches, the
// first by the name the box shows. A pick goes to onPick, and a press outside
// the pane and its control closes it (onClose); Escape is the box's. The pane
// takes the keyboard as it opens, and the arrows move through the choices.
export function EnginePane({ setting, harness, model, effort, control, onPick, onClose }: {
  setting: EngineSetting; harness: string; model: string; effort: string;
  control: RefObject<HTMLButtonElement | null>;
  onPick: (value: string, chosen?: EngineModel) => void; onClose: () => void;
}) {
  const pane = useRef<HTMLDivElement>(null);
  const catalog = useResource("/api/engines", parseEngineCatalog);
  const offered = catalog.data?.find((item) => item.id === harness);
  const models = offered?.models ?? [];
  const running = models.find((listed) => listed.id === model) ?? models.find((listed) => !!model && shortModel(listed.id) === shortModel(model));
  const efforts = effortsInOrder(running?.efforts.length ? running.efforts : [...new Set(models.flatMap((listed) => listed.efforts))]);
  const choices = setting === "model"
    ? models.map((listed) => ({ value: listed.id, label: shortModel(listed.name) || listed.name, detail: listed.id === listed.name ? "" : listed.id, isChosen: listed === running, tone: "", chosen: listed }))
    : efforts.map((level) => ({ value: level, label: level, detail: "", isChosen: level === effort, tone: effortTone(level), chosen: undefined }));
  const isReady = choices.length > 0;
  // A catalog that cannot be read goes to the CFO, as the board's errors do,
  // and the pane offers Retry.
  useEffect(() => { if (catalog.error) reportToCfo("the session's " + setting + " choices", catalog.error); }, [catalog.error, setting]);
  useEffect(() => {
    const items = Array.from(pane.current?.querySelectorAll<HTMLElement>("[role=menuitemradio]") ?? []);
    (items.find((item) => item.getAttribute("aria-checked") === "true") ?? items[0] ?? pane.current)?.focus();
  }, [isReady]);
  useEffect(() => {
    const away = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!pane.current?.contains(target) && !control.current?.contains(target)) onClose();
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [control, onClose]);
  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    const items = Array.from(pane.current?.querySelectorAll<HTMLElement>("[role=menuitemradio]") ?? []);
    const at = items.indexOf(document.activeElement as HTMLElement);
    items[(at + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length]?.focus();
  };
  const title = setting === "model" ? "Model" : "Effort";
  return <div ref={pane} className="popup-surface engine-pane" role="menu" aria-label={title} tabIndex={-1} onKeyDown={keys}>
    <p className="engine-pane-title"><HarnessMark harness={harness} />{title}</p>
    {catalog.error ? <button type="button" className="labelled-button engine-pane-retry" onClick={catalog.reload}><Icon name="refresh" />Retry</button>
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
