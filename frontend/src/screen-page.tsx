import { useEffect } from "react";
import { useRuntimeStream } from "./stream";
import { goblinName } from "./task-words";
import { ScreenView } from "./screen-view";

// ScreenPage is a goblin's Screen view at full size in a tab of its own, the
// address its card's Browser active opens (screenAddress): the same view as
// the panel's, with the board's live record saying whose browser it is and
// whether it still runs.
export function ScreenPage({ task: id }: { task: string }) {
  const { snapshot, connection } = useRuntimeStream();
  const task = snapshot?.tasks.find((candidate) => candidate.id === id);
  const name = task ? goblinName(task) : id;
  useEffect(() => { document.title = name + "'s browser · Code Goblins"; }, [name]);
  return <div className="screen-page-shell">
    <header>
      <a className="brand-mark" href="/" aria-label="Code Goblins board"><img src="/assets/goblin-app.png" width="32" height="32" alt="" /></a>
      <h1>{name}'s browser</h1>
    </header>
    <main aria-label={name + "'s browser"}>
      {!snapshot ? <p className="muted" role="status">Connecting to the board</p>
        : !task ? <p role="status">No goblin on the board has the task {id}.</p>
        : !task.browser ? <p role="status">{name}'s browser is not open. It opens when {name} uses its browser.</p>
        : <ScreenView key={task.generation} task={task.id} generation={task.generation} name={name} instance={snapshot.instance} connected={connection === "Live"} isFull />}
    </main>
  </div>;
}
