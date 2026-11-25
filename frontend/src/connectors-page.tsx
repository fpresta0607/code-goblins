import { useEffect, useMemo, useRef, useState } from "react";
import { ago, type BrowserLogin } from "./browser-logins";
import { ClickFeedback } from "./click-feedback";
import { useNow } from "./clock";
import { ConnectorCrumbs } from "./connector-crumbs";
import { ConnectorGoblinView } from "./connector-goblin-view";
import { ConnectorGoblinsView } from "./connector-goblins-view";
import { ConnectorHome } from "./connector-home";
import { ConnectorPcView } from "./connector-pc-view";
import { crumbsTo, HOME, placeKey, type Place } from "./connector-place";
import { ConnectorProjectView } from "./connector-project-view";
import { ConnectorProjectsView } from "./connector-projects-view";
import { allRows, oldestCheck } from "./connector-rows";
import { ConnectorSearchView } from "./connector-search-view";
import { isAnyChecking } from "./connector-snapshot";
import type { ConnectorViewProps } from "./connector-view-props";
import { ForgetLoginDialog } from "./forget-login-dialog";
import { Icon } from "./Icon";
import { Notice } from "./notice";
import { goblinName } from "./task-words";
import type { Snapshot } from "./types";
import { useConnectors } from "./use-connectors";
import "./connectors-page.css";

// The Connectors page as a drill-down (#2902): This PC and its accounts, then
// each project, then each goblin, each level checked once and saved, so the
// page and its search open from the saved snapshot at once and start nothing.
// Its home has a tile for each level and Needs a look, every item of any level
// that is in trouble. Every row says which level it comes from, when it was
// checked, and Stale or Rechecking when that is so. Search runs over the whole
// snapshot in the browser, grouped by level. It replaced the page that read
// each goblin's connections with a second harness session of its own (the
// Overlord, 2026-11-10, #2303: the connectors panel "doesn't look like Codex
// or Claude Code's connectors panel").
export function ConnectorsPage({ snapshot, onRepair }: { snapshot: Snapshot; onRepair: (key: string) => void }) {
  const running = useMemo(() => snapshot.tasks.filter((task) => task.generation && !task.archived), [snapshot.tasks]);
  const { view, isUnreadable, isCapped, feedback, repair, actions, readAgain } = useConnectors(running, snapshot.runs || [], snapshot.instance, onRepair);
  const [place, setPlace] = useState<Place>(HOME);
  const [search, setSearch] = useState("");
  const [forgetting, setForgetting] = useState<{ login: BrowserLogin; project: string; task: string; generation: string }>();
  const now = useNow(60_000);
  const names = useMemo(() => new Map(snapshot.tasks.map((task) => [task.id, goblinName(task)])), [snapshot.tasks]);
  const nameOf = (task: string) => names.get(task) ?? task;
  const rows = useMemo(() => view ? allRows(view, (task) => names.get(task) ?? task) : [], [view, names]);
  // Going back lands on the row the way down was taken from, and going down
  // lands on the view, so the keyboard is never left on what went away.
  const region = useRef<HTMLElement>(null);
  const [arrival, setArrival] = useState<string>();
  const go = (next: Place) => {
    setArrival(placeKey(place));
    setSearch("");
    setPlace(next);
  };
  useEffect(() => {
    if (arrival === undefined) return;
    const from = [...region.current?.querySelectorAll<HTMLElement>("[data-place]") ?? []].find((row) => row.dataset.place === arrival);
    (from?.querySelector<HTMLElement>(".connector-open") ?? from ?? region.current)?.focus();
  }, [place, arrival]);
  const isSearching = search.trim() !== "";
  const goblin = place.kind === "goblin" ? view?.goblins.find((each) => each.task === place.task) : undefined;
  const crumbs = crumbsTo(place, { name: place.kind === "goblin" ? nameOf(place.task) : "", project: goblin?.project ?? "" });
  // The question goes with the goblin it would act through: one that restarted
  // or left is not asked about.
  const forgotten = forgetting && view?.goblins.some((each) => each.task === forgetting.task && each.generation === forgetting.generation) ? forgetting : undefined;
  const oldest = view ? oldestCheck(view) : "";
  const status = !view ? isUnreadable ? "" : "Reading the saved snapshot..."
    : isCapped ? "Still checking." : isAnyChecking(view) ? "Rechecking in the background..."
    : oldest ? "Saved snapshot, oldest check " + ago(oldest, now) : "Nothing has been checked yet";
  const props = view && { snapshot: view, rows, now, actions, nameOf, onOpen: go } satisfies ConnectorViewProps;
  return <div className="connectors-page">
    <header className="connectors-head">
      <h2>Connectors</h2>
      <p role="status">{status}</p>
      {isCapped && <button type="button" onClick={readAgain}>Read again</button>}
      <label className="connectors-search"><Icon name="search" /><input type="search" value={search} placeholder="Search every level" aria-label="Search connectors" onChange={(event) => setSearch(event.currentTarget.value)} /></label>
    </header>
    <ClickFeedback text={feedback} />
    {isUnreadable && <Notice tone="warning">Connectors unavailable</Notice>}
    {repair.notice && <div className="connector-notice">
      <p role="status">{repair.notice}</p>
      {repair.runId && <button type="button" onClick={() => onRepair("run:" + repair.runId)}>Open repair card</button>}
      {repair.signIn && <a href={repair.signIn} target="_blank" rel="noreferrer" onClick={() => actions.expectSignIn(place.kind === "project" ? place.project : "")}>Open sign-in</a>}
    </div>}
    {props && <>
      <ConnectorCrumbs crumbs={isSearching ? [{ label: "Search: every level", place: HOME }] : crumbs} onOpen={go} />
      <section ref={region} className="connector-view" aria-label={(isSearching ? "Search" : crumbs[crumbs.length - 1].label) + " view"} tabIndex={-1}>
        {isSearching ? <ConnectorSearchView {...props} search={search} />
          : place.kind === "pc" ? <ConnectorPcView {...props} />
          : place.kind === "projects" ? <ConnectorProjectsView {...props} />
          : place.kind === "project" ? <ConnectorProjectView {...props} project={place.project} onForget={(login, project) => {
            const actor = view?.goblins.find((each) => each.project === project);
            if (actor) setForgetting({ login, project, task: actor.task, generation: actor.generation });
          }} />
          : place.kind === "goblins" ? <ConnectorGoblinsView {...props} />
          : place.kind === "goblin" ? <ConnectorGoblinView {...props} task={place.task} />
          : <ConnectorHome {...props} />}
      </section>
    </>}
    {forgotten && <ForgetLoginDialog login={forgotten.login} onCancel={() => setForgetting(undefined)} onForget={() => { setForgetting(undefined); actions.endLogin(forgotten.project, forgotten.login.id, "forget"); }} />}
  </div>;
}
