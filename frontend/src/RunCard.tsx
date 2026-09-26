import { useState, type ReactNode } from "react";
import type { Run } from "./types";
import { Icon } from "./Icon";
import { ConnectorMark } from "./ConnectorMark";
import { Disclosure } from "./Disclosure";
import { runMark } from "./feedback";
import { shellLabel, shellMark } from "./connectors";

// A command the CFO needs the Overlord to run: why, the shell, the exact text
// that runs and where, one Run button, its live state and the captured output.
// The browser never sends the command; Run names the stored item.
export function RunCard({ run, connected, sending, error, onRun, pager }: { run: Run; connected: boolean; sending: boolean; error: string; onRun: () => void; pager?: ReactNode }) {
  const [copied, setCopied] = useState(false);
  const mark = runMark(run);
  const copy = () => navigator.clipboard.writeText(run.command).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1400); }, () => {});
  return <article className="run-card" aria-labelledby={"run-" + run.id}>
    <header className="run-head">
      <ConnectorMark mark={shellMark(run.shell)} label={shellLabel(run.shell)} />
      <span className="chip">{shellLabel(run.shell)}</span>
      {run.admin && <span className="chip admin" data-tip="Runs as administrator"><Icon name="shield" />Admin</span>}
      <span className={"run-state delivery " + (mark.trouble ? "failed" : "succeeded")} role="status"><Icon name={mark.icon} />{mark.label}</span>
    </header>
    <h3 id={"run-" + run.id}>{run.title}</h3>
    <div className="run-command">
      <pre><code>{run.command}</code></pre>
      <button type="button" className="icon-button" aria-label="Copy command" data-tip={copied ? "Copied" : "Copy command"} data-tip-align="end" onClick={copy}><Icon name={copied ? "check" : "copy"} /></button>
    </div>
    {run.cwd && <p className="run-cwd"><Icon name="folder" /><span>{run.cwd}</span></p>}
    {run.reason && <p className={mark.trouble ? "warning-text" : "muted"}>{run.reason}</p>}
    {error && <p className="warning-text" role="alert">{error}</p>}
    {(run.state === "ready" || pager) && <div className="card-actions">
      {pager}
      {run.state === "ready" && <>
        {run.admin && <small className="muted">Windows will ask you to confirm.</small>}
        <button className="primary run-button" type="button" disabled={!connected || sending} onClick={onRun}><Icon name="play" />{run.admin ? "Run as administrator" : "Run"}</button>
      </>}
    </div>}
    {run.output && <Disclosure kind="run-output" title={<>Output{run.exit_code !== null && <span className="column-count">exit {run.exit_code}</span>}</>}>
      <pre className="run-output">{run.output}</pre>
    </Disclosure>}
  </article>;
}
