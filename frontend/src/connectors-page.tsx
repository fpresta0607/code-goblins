import { useEffect, useMemo, useRef, useState } from "react";
import { ago, type BrowserLogin } from "./browser-logins";
import { ClickFeedback } from "./click-feedback";
import { useNow } from "./clock";
import { ConnectorCrumbs } from "./connector-crumbs";
import { ConnectorGoblinView } from "./connector-goblin-view";
import { ConnectorLookView } from "./connector-look-view";
import { ConnectorPcView } from "./connector-pc-view";
import { crumbsTo, HOME, placeKey, type Place } from "./connector-place";
import { ConnectorProjectView } from "./connector-project-view";
import { ConnectorProjectsView } from "./connector-projects-view";
import { allRows, oldestCheck } from "./connector-rows";
import { ConnectorSearchView } from "./connector-search-view";
import { isAnyChecking } from "./connector-snapshot";
import { ConnectorTree } from "./connector-tree";
import type { ConnectorViewProps } from "./connector-view-props";
import { ConnectorYoursView } from "./connector-yours-view";
import { ForgetLoginDialog } from "./forget-login-dialog";
import { Icon } from "./Icon";
import { Notice } from "./notice";
import { goblinName } from "./task-words";
import type { Snapshot } from "./types";
import { useConnectors } from "./use-connectors";
import "./connectors-page.css";

// The Connectors page as a tree of where connectors live (#3010, after the
// drill-down of #2902): This PC, which every goblin gets, then each project,
// one git repository, with its goblins under it, then the Overlord's own
// Claude and Codex, each checked once and saved, so the page and its search
// open from the saved snapshot at once and start nothing. Where the page is
// wide the tree stays at its left and the place it opens shows beside it,
// Needs a look at the home; on a phone the tree is the home and each place
// opens as its own page. Every row says which scope it lives in, when it was
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
  // Going back up lands on the row the way down was taken from, in the tree
  // or the view, whichever it was taken in, and going down or across lands on
  // the view, so the keyboard is never left on what went away. The tree is the
  // home, so what it opens was opened from the home.
  const region = useRef<HTMLElement>(null);
  const tree = useRef<HTMLDivElement>(null);
  const [waysDown, setWaysDown] = useState<Record<string, { from: string; pane: "tree" | "view" }>>({});
  const [arrival, setArrival] = useState<string>();
  const go = (next: Place, pane: "tree" | "view" = "view") => {
    const path = crumbs.map((crumb) => placeKey(crumb.place));
    const upTo = path.indexOf(placeKey(next));
    const isUp = upTo >= 0 && upTo < path.length - 1;
    if (!isUp) setWaysDown((ways) => ({ ...ways, [placeKey(next)]: { from: placeKey(pane === "tree" ? HOME : place), pane } }));
    setArrival(isUp ? path.slice(upTo + 1).reverse().find((key) => waysDown[key]?.from === placeKey(next)) ?? "" : "");
    setSearch("");
    setPlace(next);
  };
  useEffect(() => {
    if (arrival === undefined) return;
    const parts = waysDown[arrival]?.pane === "tree" ? [tree.current, region.current] : [region.current, tree.current];
    const shown = parts.flatMap((part) => [...part?.querySelectorAll<HTMLElement>("[data-place]") ?? []]);
    const from = arrival ? shown.find((row) => row.dataset.place === arrival && row.getClientRects().length > 0) : undefined;
    (from?.querySelector<HTMLElement>(".connector-open") ?? from ?? region.current)?.focus();
  }, [place, arrival, waysDown]);
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
    {props && <div className="connectors-split" data-shows={place.kind === "home" && !isSearching ? "tree" : "place"}>
      <div ref={tree} className="connectors-split-tree"><ConnectorTree snapshot={props.snapshot} rows={rows} nameOf={nameOf} place={place} onOpen={(next) => go(next, "tree")} /></div>
      <div className="connectors-split-place">
        {(isSearching || place.kind !== "home") && <ConnectorCrumbs crumbs={isSearching ? [{ label: "Search: every level", place: HOME }] : crumbs} onOpen={go} />}
        <section ref={region} className="connector-view" aria-label={(isSearching ? "Search" : crumbs[crumbs.length - 1].label) + " view"} tabIndex={-1}>
          {isSearching ? <ConnectorSearchView {...props} search={search} />
            : place.kind === "pc" ? <ConnectorPcView {...props} />
            : place.kind === "yours" ? <ConnectorYoursView {...props} />
            : place.kind === "projects" ? <ConnectorProjectsView {...props} />
            : place.kind === "project" ? <ConnectorProjectView {...props} project={place.project} onForget={(login, project) => {
              const actor = view?.goblins.find((each) => each.project === project);
              if (actor) setForgetting({ login, project, task: actor.task, generation: actor.generation });
            }} />
            : place.kind === "goblin" ? <ConnectorGoblinView {...props} task={place.task} />
            : <ConnectorLookView {...props} />}
        </section>
      </div>
    </div>}
    {forgotten && <ForgetLoginDialog login={forgotten.login} onCancel={() => setForgetting(undefined)} onForget={() => { setForgetting(undefined); actions.endLogin(forgotten.project, forgotten.login.id, "forget"); }} />}
  </div>;
}
