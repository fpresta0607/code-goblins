import { useEffect, useRef, useState, type ReactNode } from "react";
import { request } from "./api";
import { copyText } from "./clipboard";
import { object, string, type Run } from "./types";
import { Icon } from "./Icon";
import { ConnectorMark } from "./ConnectorMark";
import { runMark } from "./feedback";
import { shellLabel, shellMark } from "./connectors";

// A command the CFO needs the Overlord to run: why, the shell, the exact text
// that runs and where, one Run button, its live state, and its output as a
// terminal shows it: read every second while it runs, then with its exit
// code. The browser never sends the command; Run names the stored item.
const OUTPUT_READ_MS = 1000;
export function RunCard({ run, connected, sending, error, onRun, pager }: { run: Run; connected: boolean; sending: boolean; error: string; onRun: () => void; pager?: ReactNode }) {
  const [copied, setCopied] = useState(false);
  const [printed, setPrinted] = useState("");
  const screen = useRef<HTMLPreElement>(null);
  const running = run.state === "running";
  useEffect(() => {
    if (!running) return;
    let stopped = false;
    const read = async () => {
      try {
        const answer = object(await request("/api/runs/" + encodeURIComponent(run.id) + "/output"));
        if (!stopped) setPrinted(string(answer.output));
      } catch {
        // The next read tries again; the run's state still shows above.
      }
    };
    void read();
    const timer = setInterval(() => void read(), OUTPUT_READ_MS);
    return () => { stopped = true; clearInterval(timer); };
  }, [running, run.id]);
  const output = running ? printed : run.output;
  // The screen follows the newest output, as a terminal does.
  useEffect(() => { if (screen.current) screen.current.scrollTop = screen.current.scrollHeight; }, [output]);
  const mark = runMark(run);
  const copy = () => copyText(run.command).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1400); }, () => {});
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
    {(running || run.output) && <section className="run-terminal" aria-label="Command output">
      <header><span>{running ? "Running" : "Output"}</span>{run.exit_code !== null && <span className={"exit-code" + (run.exit_code === 0 ? " succeeded" : " failed")}>exit {run.exit_code}</span>}</header>
      <pre ref={screen} className="run-output">{output || (running ? "Waiting for output" : "")}</pre>
    </section>}
  </article>;
}
