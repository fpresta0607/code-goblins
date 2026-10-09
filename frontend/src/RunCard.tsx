import { useEffect, useState, type ReactNode } from "react";
import { copyText } from "./clipboard";
import { type Run, type Task } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { ConnectorMark } from "./ConnectorMark";
import { runFailure, runMark } from "./feedback";
import { runLabel, shellLabel, shellMark } from "./connectors";
import { age } from "./presentation";
import { personaFor } from "./workflow";
import { RunTerminal } from "./run-terminal";
import { ClickFeedback, useClickFeedback } from "./click-feedback";

// A command the CFO, or a goblin, needs the Overlord to run: who asks when it
// is a goblin's own, why, the shell, the exact text that runs and where, and
// one Run button that says where it runs. Run turns the card into the
// command's own terminal, in place of the notice it once showed: he types
// there and it completes by itself, Complete, or Failed with one line saying
// why and who takes the next step, keeping the end of what its terminal
// showed. An administrator's
// command shows its terminal once he confirmed Windows' own prompt. The
// browser never sends the command; Run names the stored item.
export function RunCard({ run, goblin, connected, instance, sending, error, onRun, pager }: { run: Run; goblin?: Task; connected: boolean; instance: string; sending: boolean; error: string; onRun: () => void; pager?: ReactNode }) {
  const [feedback, showFeedback] = useClickFeedback();
  useEffect(() => { if (error) showFeedback(error); }, [error, showFeedback]);
  const [copied, setCopied] = useState(false);
  const running = run.state === "running";
  const mark = runMark(run);
  const failure = runFailure(run);
  const copy = () => copyText(run.command).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1400); }, () => {});
  return <article className={"run-card" + (running && run.terminal ? " live" : "")} aria-labelledby={"run-" + run.id}>
    {run.task && <p className="asker"><Avatar persona={personaFor(goblin)} small /><span><strong>{goblin?.title || run.task}</strong> asks you to run this · {run.state === "ready" ? "waiting " + age(run.created_at).replace(/ ago$/, "") : "asked " + age(run.created_at)}</span></p>}
    <header className="run-head">
      <ConnectorMark mark={shellMark(run.shell)} label={shellLabel(run.shell)} />
      <span className="chip">{shellLabel(run.shell)}</span>
      {run.admin && <span className="chip admin" data-tip="Runs as administrator"><Icon name="shield" />Admin</span>}
      <span className={"run-state delivery " + (mark.trouble ? "failed" : "succeeded")} role="status"><Icon name={mark.icon} />{failure ? "Failed" : mark.label}</span>
    </header>
    <h3 id={"run-" + run.id}>{run.title}</h3>
    {failure && <p className="run-failure">{failure}</p>}
    <div className="run-command">
      <pre><code>{run.command}</code></pre>
      <button type="button" className="icon-button" aria-label="Copy command" data-tip={copied ? "Copied" : "Copy command"} data-tip-align="end" onClick={copy}><Icon name={copied ? "check" : "copy"} /></button>
    </div>
    {run.cwd && <p className="run-cwd"><Icon name="folder" /><span>{run.cwd}</span></p>}
    {run.reason && !mark.trouble && !mark.label.includes(run.reason) && <p className="muted">{run.reason}</p>}
    {running && (run.terminal
      ? <RunTerminal run={run} instance={instance} connected={connected} />
      : <p className="muted run-waiting" role="status"><Icon name={run.admin ? "shield" : "terminal"} />{run.admin ? "Confirm the Windows prompt to run it as administrator. It runs here once you do." : "Starting its terminal"}</p>)}
    {!running && run.output && <section className="run-terminal" aria-label="Command output">
      <header><span>Output</span></header>
      <pre className="run-output">{run.output}</pre>
    </section>}
    {(run.state === "ready" || pager) && <div className="card-actions">
      <ClickFeedback text={feedback} />
      {pager}
      {run.state === "ready" && <>
        {run.admin && <small className="muted">Windows will ask to confirm.</small>}
        <button className="primary run-button" type="button" disabled={!connected || sending} onClick={onRun}><Icon name="play" />{runLabel(run.shell, run.admin)}</button>
      </>}
    </div>}
  </article>;
}
