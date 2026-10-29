import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { useEngineCatalog, type EngineModel } from "./engine-catalog";
import { effortsInOrder, effortTone, MODES, type EngineSetting } from "./engine-choices";
import { HarnessMark } from "./harness-mark";
import { Icon } from "./Icon";
import { shortModel } from "./model-name";
import "./engine-pane.css";

// EnginePane is Modern Chat's one pane for a session's settings, opened from
// the model button in the box (the Overlord, 2026-10-28: "have the selected
// model at the very top and checked. That way you can keep it all in one
// little screen and one little modal"). It lists what the session's harness
// really offers, from the catalog the board read ahead (useEngineCatalog), so
// it opens at once: the model the session runs at the top, checked, then the
// others, each with its efforts in a flyout off its side, each in its level's
// colour, that comes out from the model he points at, focuses or taps (2026-10-
// 28: "a horizontal model stems off the side of the hovered or clicked model");
// and, while the session's terminal names one, its permission mode, each in
// a colour of its own. A pick goes to onPick with the model and the effort it
// sets, and a mode to onMode; a press outside the pane and its control closes
// it (onClose); Escape is the box's. The pane takes the keyboard as it opens,
// on the model it runs, or with setting effort on that model's efforts; the
// arrows move through the choices, and right and left into a model's efforts
// and back. A mouse's press on a model, or one on a model whose flyout is
// out, is its pick, so a tap, which cannot point, first brings out its
// efforts; only a mouse on a screen that can point brings one out by passing
// over a model, never the pointer a phone's browser moves under a tap as the
// page changes. The press reads the pointer, not the flyout, as a mouse's
// pass and press come too close together for the flyout to be drawn.
export function EnginePane({ setting, harness, model, effort, mode, control, onPick, onMode, onClose }: {
  setting: EngineSetting; harness: string; model: string; effort: string; mode: string;
  control: RefObject<HTMLButtonElement | null>;
  onPick: (pick: { model?: EngineModel; effort?: string }) => void; onMode: (mode: string) => void; onClose: () => void;
}) {
  const panes = useRef<HTMLDivElement>(null);
  const pane = useRef<HTMLDivElement>(null);
  const flyoutPane = useRef<HTMLDivElement>(null);
  const { data: catalog, error: catalogError, reload } = useEngineCatalog(true);
  // A catalog the board could not read is read again once as the pane
  // opens, which offers Retry while it still cannot.
  const hasRetried = useRef(false);
  useEffect(() => {
    if (catalogError && !hasRetried.current) reload();
    hasRetried.current = true;
  }, [catalogError, reload]);
  const offered = catalog?.find((item) => item.id === harness);
  const models = offered?.models ?? [];
  const running = models.find((listed) => listed.id === model) ?? models.find((listed) => !!model && shortModel(listed.id) === shortModel(model));
  const listed = running ? [running, ...models.filter((other) => other !== running)] : models;
  // The model whose efforts are out, and the one whose flyout was out when
  // a press on it began.
  const [flyout, setFlyout] = useState<string | null>(null);
  const pressed = useRef("");
  const shown = listed.find((each) => each.id === flyout);
  const shouldFocusEffort = useRef(false);
  const isListed = listed.length > 0, hasEfforts = !!listed[0]?.efforts.length;
  useEffect(() => {
    shouldFocusEffort.current = setting === "effort" && hasEfforts;
    (pane.current?.querySelector<HTMLElement>("[role=menuitemradio]") ?? pane.current)?.focus({ preventScroll: true });
  }, [isListed, hasEfforts, setting]);
  // The flyout sits beside its model's row, kept above the pane's foot so it
  // never covers the box.
  const place = () => {
    const out = flyoutPane.current, row = flyout && pane.current?.querySelector<HTMLElement>(`[data-model="${CSS.escape(flyout)}"]`), around = panes.current;
    if (!out || !row || !around) return;
    out.style.top = Math.min(row.getBoundingClientRect().top - around.getBoundingClientRect().top, around.clientHeight - out.offsetHeight) + "px";
  };
  useLayoutEffect(() => {
    place();
    if (!shouldFocusEffort.current || !flyoutPane.current) return;
    shouldFocusEffort.current = false;
    const efforts = Array.from(flyoutPane.current.querySelectorAll<HTMLElement>("[role=menuitemradio]"));
    (efforts.find((item) => item.getAttribute("aria-checked") === "true") ?? efforts[0])?.focus();
  });
  useEffect(() => {
    const away = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!panes.current?.contains(target) && !control.current?.contains(target)) onClose();
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [control, onClose]);
  const keys = (event: KeyboardEvent<HTMLDivElement>) => {
    const within = flyoutPane.current?.contains(document.activeElement) ? flyoutPane.current : pane.current;
    const items = Array.from(within?.querySelectorAll<HTMLElement>("[role=menuitemradio]") ?? []);
    const at = items.indexOf(document.activeElement as HTMLElement);
    if (event.key === "ArrowRight" && within === pane.current && flyout && items[at]?.dataset.model === flyout) {
      const efforts = Array.from(flyoutPane.current?.querySelectorAll<HTMLElement>("[role=menuitemradio]") ?? []);
      (efforts.find((item) => item.getAttribute("aria-checked") === "true") ?? efforts[0])?.focus();
    } else if (event.key === "ArrowLeft" && within === flyoutPane.current && flyout) {
      pane.current?.querySelector<HTMLElement>(`[data-model="${CSS.escape(flyout)}"]`)?.focus();
    } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      items[(at + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length]?.focus();
    } else return;
    event.preventDefault();
  };
  const choice = (key: string, label: string, isChosen: boolean, tone: string, pick: () => void) => <button key={key} type="button" role="menuitemradio" aria-checked={isChosen} className="engine-choice" onClick={pick}>
    <span className={"engine-choice-label" + (tone ? " effort-level " + tone : "")}>{label}</span>
    {isChosen && <Icon name="check" />}
  </button>;
  const modes = MODES.some((each) => each.mode === mode) ? MODES : [];
  return <div ref={panes} className="engine-panes" onKeyDown={keys}>
    <div ref={pane} className="popup-surface engine-pane" role="menu" aria-label="Model" tabIndex={-1} onScroll={place}>
      <p className="engine-pane-title"><HarnessMark harness={harness} />Model</p>
      {catalogError ? <button type="button" className="labelled-button engine-pane-retry" onClick={reload}><Icon name="refresh" />Retry</button>
        : !catalog ? <p className="engine-pane-note" role="status">Reading the session's models…</p>
        : offered?.reason ? <p className="engine-pane-note" role="status">{offered.reason}</p>
        : !listed.length ? <p className="engine-pane-note" role="status">This harness lists no models.</p>
        : listed.map((each) => {
          const hasEfforts = each.efforts.length > 0;
          return <button key={each.id} data-model={each.id} type="button" role="menuitemradio" aria-checked={each === running}
            aria-haspopup={hasEfforts ? "menu" : undefined} aria-expanded={hasEfforts ? flyout === each.id : undefined} className="engine-choice"
            onPointerEnter={(event) => { if (event.pointerType === "mouse" && matchMedia("(hover: hover)").matches) setFlyout(hasEfforts ? each.id : null); }}
            onPointerDown={(event) => { pressed.current = event.pointerType === "mouse" || flyout === each.id ? each.id : ""; }}
            onFocus={() => setFlyout(hasEfforts ? each.id : null)}
            onClick={(event) => {
              if (hasEfforts && (event.detail ? pressed.current !== each.id : flyout !== each.id)) setFlyout(each.id);
              else onPick({ model: each });
            }}>
            <span className="engine-choice-label">{shortModel(each.name) || each.name}</span>
            {each === running && <Icon name="check" />}
          </button>;
        })}
      {modes.length > 0 && <div className="engine-modes" role="group" aria-label="Mode">
        <p className="engine-pane-title">Mode</p>
        <div className="engine-mode-choices">
          {modes.map((each) => <button key={each.mode} type="button" role="menuitemradio" aria-checked={each.mode === mode} className={"engine-mode mode-" + each.tone}
            onPointerEnter={() => setFlyout(null)} onFocus={() => setFlyout(null)} onClick={() => onMode(each.mode)}>{each.name}</button>)}
        </div>
      </div>}
    </div>
    {shown && <div ref={flyoutPane} className="popup-surface engine-flyout" role="menu" aria-label="Effort">
      <p className="engine-pane-title">Effort</p>
      {effortsInOrder(shown.efforts).map((level) => choice(level, level, shown === running && level === effort, effortTone(level),
        () => onPick(shown === running ? { effort: level } : { model: shown, effort: level })))}
    </div>}
  </div>;
}
