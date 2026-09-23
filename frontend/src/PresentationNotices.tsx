import { useState } from "react";
import type { BoardActivity, Snapshot } from "./types";

export function PresentationNotices({snapshot,presentations}:{snapshot:Snapshot;presentations:BoardActivity[]}) {
  const [background,setBackground]=useState<Set<string>>(new Set());
  const activity=presentations.filter(event=>!background.has(event.id));
  return <section className="presentation-notices" aria-label="Browser and review updates">
    {activity.length ? activity.slice(-4).reverse().map(event=><article key={event.id}>
      <strong>{event.kind==="review" ? "Review ready" : "Browser walkthrough running"}</strong>
      <p>{event.cfo_identity?"CFO":snapshot.tasks.find(task=>task.id===event.task_id)?.title || event.task_id}</p>
      <div><a href={event.url} target="_blank" rel="noreferrer">{event.kind==="review" ? "Open review" : "Open page"}</a><button onClick={()=>setBackground(prior=>new Set([...prior,event.id]))}>Keep in background</button></div>
    </article>) : <p className="muted">No live browser or review notices.</p>}
    <small>Work continues whether you open a page or keep it in the background.</small>
  </section>;
}
