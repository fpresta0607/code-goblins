import { useState } from "react";
import { message, request } from "./api";
import { object, string, type Session, type Snapshot, type Task } from "./types";
import { Avatar } from "./Avatar";
import { BRAND_MARKS } from "./brandMarks";
import { Icon } from "./Icon";
import { ownsTaskSession, sessionTitle } from "./lineageTree";
import { asksOverlord, nodeStatus, personaFor, pullRequestBadge, pullRequestLabel, safePullRequest } from "./workflow";

// Who the goblin is, what it is doing now and what the Overlord can do about
// it. The CFO drawn without a task has no worktree to open.
export function PanelHeader({ task, node, snapshot, compact }: { task?: Task; node?: Session; snapshot: Snapshot; compact: boolean }) {
  const [opening, setOpening] = useState(false);
  const [outcome, setOutcome] = useState("");
  const cfo = !task && !node;
  const owner = !!task && ownsTaskSession(node, task);
  const asking = owner && asksOverlord(snapshot, task.id);
  const cfoSession = snapshot.sessions.find((session) => session.role === "cfo");
  const title = cfo ? "CFO" : node ? sessionTitle(node, task) : task?.title || task?.id || "";
  const status = cfo
    ? nodeStatus({ id: "cfo", title, session: cfoSession, relation: "", status: snapshot.registration ? "Registration stale" : cfoSession ? undefined : "Supervising" })
    : nodeStatus({ id: title, title, task, session: node, relation: "" }, asking);
  const phase = cfo ? (snapshot.registration ? "stale" : cfoSession?.runtime?.state || cfoSession?.phase || "working") : owner ? task.phase : node?.runtime?.state || node?.phase || "";
  const pr = owner ? safePullRequest(task.pr) : "";
  const badge = pullRequestBadge(pr);
  const open = async (target: "vscode" | "folder") => {
    if (!task || opening) return;
    setOpening(true); setOutcome("");
    try {
      const result = object(await request("/api/workspace/open", undefined, { method: "POST", headers: { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance }, body: JSON.stringify({ task: task.id, generation: task.generation, target }) }));
      setOutcome(string(result.message));
    } catch (error: unknown) { setOutcome(message(error) + " Nothing is retried automatically."); }
    finally { setOpening(false); }
  };
  return <header className={"panel-header" + (compact ? " compact" : "")}>
    <Avatar persona={cfo ? "cfo" : personaFor(task, node)} small />
    <div className="panel-identity">
      <h2 id="panel-title">{title}</h2>
      {!compact && task?.project && <p className="project-label">{task.project}</p>}
      <p className={"panel-status plain-status phase-" + phase + (asking ? " asking" : "")}><span className="status-dot" />{status}</p>
      {!compact && !owner && node && task && <p className="muted">Part of {task.title || task.id}</p>}
      {!compact && owner && task.activity && <p className="panel-activity">{task.activity}</p>}
    </div>
    {owner && !!task.generation && <div className="panel-actions">
      <button className="icon-button raised" disabled={opening || !snapshot.instance} aria-label="Open in VS Code" data-tip="Open in VS Code" data-tip-align="start" onClick={() => void open("vscode")}><img className="brand-icon" src="/assets/vscode.svg" alt="" /></button>
      <button className="icon-button raised" disabled={opening || !snapshot.instance} aria-label="Open folder" data-tip="Open folder" onClick={() => void open("folder")}><Icon name="folder" /></button>
      {pr && <a className="icon-button raised pill-link" href={pr} target="_blank" rel="noreferrer" aria-label={"Open pull request " + pullRequestLabel(pr)} data-tip="Open pull request">{badge.github ? <svg className="icon brand-glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={BRAND_MARKS.github.path} /></svg> : <Icon name="pull-request" />}<span>{badge.label}</span></a>}
    </div>}
    {outcome && <p className="workspace-outcome" role="status">{outcome}</p>}
  </header>;
}
