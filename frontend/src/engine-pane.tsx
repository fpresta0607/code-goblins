import { useEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { reportToCfo, useResource } from "./api";
import { parseEngineCatalog, type EngineModel } from "./engine-catalog";
import { effortsInOrder, effortTone, type EngineSetting } from "./engine-choices";
import { HarnessMark } from "./harness-mark";
import { Icon } from "./Icon";
import { shortModel } from "./model-name";
import "./engine-pane.css";

// EnginePane is Modern Chat's own pane for a session's model and effort (the
// Overlord, 2026-10-27: "The modern chat should have its own panels to adjust
// the settings. It shouldn't rely on any terminal views or UI"). It lists
// what the session's harness really offers, read from its own catalog
// (GET /api/engines) only while the pane is open: its models, and, as a
// drawer at the pane's foot that extends off it (the Overlord, 2026-10-28:
// "the effort selector is a drawer that extends off the model selector's
// pane"), the efforts of the model it runs, each in its level's colour. The
// effort control opens the pane with the drawer out (setting). The one it
// runs is checked: the model whose id the session names, or, when no id
// matches, the first by the name the box shows. A pick goes to onPick with
// what it sets, and a press outside the pane and its control closes it
// (onClose); Escape is the box's. The pane takes the keyboard as it opens,
// and the arrows move through the choices.
export function EnginePane({ setting, harness, model, effort, control, onPick, onClose }: {
  setting: EngineSetting; harness: string; model: string; effort: string;
  control: RefObject<HTMLButtonElement | null>;
  onPick: (setting: EngineSetting, value: string, chosen?: EngineModel) => void; onClose: () => void;
}) {
  const pane = useRef<HTMLDivElement>(null);
  const catalog = useResource("/api/engines", parseEngineCatalog);
  const offered = catalog.data?.find((item) => item.id === harness);
  const models = offered?.models ?? [];
  const running = models.find((listed) => listed.id === model) ?? models.find((listed) => !!model && shortModel(listed.id) === shortModel(model));
  const efforts = effortsInOrder(running?.efforts.length ? running.efforts : [...new Set(models.flatMap((listed) => listed.efforts))]);
  const [isDrawerOpen, setDrawerOpen] = useState(setting === "effort");
  const isReady = setting === "model" ? models.length > 0 : efforts.length > 0;
  // A catalog that cannot be read goes to the CFO, as the board's errors do,
  // and the pane offers Retry.
  useEffect(() => { if (catalog.error) reportToCfo("the session's " + setting + " choices", catalog.error); }, [catalog.error, setting]);
  useEffect(() => {
    const within = setting === "effort" ? pane.current?.querySelector(".engine-drawer") : pane.current;
    const items = Array.from(within?.querySelectorAll<HTMLElement>("[role=menuitemradio]") ?? []);
    (items.find((item) => item.getAttribute("aria-checked") === "true") ?? items[0] ?? pane.current)?.focus();
  }, [isReady, setting]);
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
  const choice = (key: string, label: string, isChosen: boolean, tone: string, pick: () => void) => <button key={key} type="button" role="menuitemradio" aria-checked={isChosen} className="engine-choice" onClick={pick}>
    <span className={"engine-choice-label" + (tone ? " effort-level " + tone : "")}>{label}</span>
    {isChosen && <Icon name="check" />}
  </button>;
  const shownEffort = efforts.includes(effort) ? effort : "";
  return <div ref={pane} className="popup-surface engine-pane" role="menu" aria-label="Model" tabIndex={-1} onKeyDown={keys}>
    <p className="engine-pane-title"><HarnessMark harness={harness} />Model</p>
    {catalog.error ? <button type="button" className="labelled-button engine-pane-retry" onClick={catalog.reload}><Icon name="refresh" />Retry</button>
      : !catalog.data ? <p className="engine-pane-note" role="status">Reading the session's models…</p>
      : offered?.reason ? <p className="engine-pane-note" role="status">{offered.reason}</p>
      : <>
        {models.length ? models.map((listed) => choice(listed.id, shortModel(listed.name) || listed.name, listed === running, "", () => onPick("model", listed.id, listed)))
          : <p className="engine-pane-note" role="status">This harness lists no models.</p>}
        <div className="engine-drawer">
          <button type="button" className="engine-drawer-toggle" aria-expanded={isDrawerOpen} onClick={() => setDrawerOpen((prior) => !prior)}>
            <span>Effort</span>{shownEffort && <span className={"effort-level " + effortTone(shownEffort)}>{shownEffort}</span>}<Icon name="chevron" />
          </button>
          {isDrawerOpen && <div className="engine-drawer-levels" role="menu" aria-label="Effort">
            {efforts.length ? efforts.map((level) => choice(level, level, level === effort, effortTone(level), () => onPick("effort", level)))
              : <p className="engine-pane-note" role="status">This model takes no effort setting.</p>}
          </div>}
        </div>
      </>}
  </div>;
}
