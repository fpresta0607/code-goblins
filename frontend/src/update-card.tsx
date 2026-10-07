import { useEffect, useState, type ReactNode } from "react";
import { request } from "./api";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { object, string, type ReleaseOffer, type Run } from "./types";
import { updateOutcome, updateProgress } from "./update-progress";

// How often the card reads what a running update printed, as a run card does.
const OUTPUT_READ_MS = 1000;
// The Update Code Goblins item: an event, not a goblin's question. It names
// the version this board runs and the one it installs, what is new in a few
// lines, whether the release is signed and what the update checks, and one
// Update button, the Overlord's alone. Once pressed it follows the update as
// a run card follows its command, step by step from what it prints, and ends
// on how it went in one line; once updated, the page reloads on the new board.
export function UpdateCard({ run, offer, connected, sending, error, onRun, onRetry, pager }: { run: Run; offer: ReleaseOffer; connected: boolean; sending: boolean; error: string; onRun: () => void; onRetry?: () => void; pager?: ReactNode }) {

  const [printed, setPrinted] = useState("");
  const running = run.state === "running";
  useEffect(() => {
    if (!running) return;
    let stopped = false;
    const read = async () => {
      try {
        const answer = object(await request("/api/runs/" + encodeURIComponent(run.id) + "/output"));
        if (!stopped) setPrinted(string(answer.output));
      } catch {
        // The board restarts during an update; the next read tries again.
      }
    };
    void read();
    const timer = setInterval(() => void read(), OUTPUT_READ_MS);
    return () => { stopped = true; clearInterval(timer); };
  }, [running, run.id]);
  const updated = run.state === "succeeded";
  const output = running ? printed : run.output;
  const progress = updateProgress(run, output);
  const outcome = updateOutcome(run);
  const tone = outcome.trouble ? " failed" : run.state === "ready" || updated ? " succeeded" : " updating";
  const when = run.state === "ready" ? "published " + age(offer.published) : running ? "updating since " + age(run.ran_at || run.created_at).replace(/ ago$/, "") : updated ? "updated " + age(run.finished_at) : "tried " + age(run.ran_at || run.created_at);
  return <article className="update-card" aria-labelledby={"update-" + run.id}>
    <header className="update-head">
      <Avatar persona="releases" small />
      <span className="who"><strong>Code Goblins</strong><small>{when}</small></span>
      <span className="chip release">New version</span>
      <span className={"run-state delivery" + tone} role="status"><Icon name={outcome.icon} />{outcome.label}</span>
    </header>
    <h3 id={"update-" + run.id}>Update Code Goblins</h3>
    <div className="update-versions" aria-label={updated ? offer.to : "From " + offer.from + " to " + offer.to}>
      {!updated && <><span className="from">{offer.from}</span><Icon name="next" /></>}
      <span className="to">{offer.to}</span>
    </div>
    {run.state === "ready" && <>
      {offer.notes.length > 0 && <ul className="update-notes">{offer.notes.map((note) => <li key={note}>{note}</li>)}</ul>}
      {offer.page && <a className="update-notes-link" href={offer.page} target="_blank" rel="noreferrer">What's new<Icon name="external" /></a>}
      <p className="update-trust"><Icon name="shield" /><span>{trustLine(offer.signing, offer.publisher)}{offer.sum && <>, such as cfo.exe <code>{offer.sum.slice(0, 8) + "…" + offer.sum.slice(-6)}</code></>}.</span></p>
      <p className="update-effect">The board restarts for a few seconds; goblins and the CFO keep running. A build that does not start is rolled back.</p>
    </>}
    {(running || run.state === "failed") && <ol className="update-steps">{progress.steps.map((step) => <li key={step.title} className={step.state}>
      <Icon name={step.state === "done" ? "check" : step.state === "now" ? "refresh" : step.state === "failed" ? "warning" : "clock"} />{step.title}
    </li>)}</ol>}
    {running && <p className="update-effect">The board is away for a few seconds and reconnects by itself.</p>}
    {progress.result && !running && <p className={"update-result " + (updated ? "ok" : "back")}>{updated ? offer.to + " runs. The board reloads on it now." : progress.result}</p>}
    {updated && <p className="update-effect">The desktop app moves to {offer.to} once you quit it from its tray icon and open it again.</p>}
    {run.state === "withdrawn" && run.reason && <p className="update-effect">{run.reason[0].toUpperCase() + run.reason.slice(1)}.</p>}
    {error && <p className="warning-text" role="alert">{error}</p>}
    {(running || output) && <details className="update-output"><summary>Output</summary><pre className="run-output">{output || "Waiting for output"}</pre></details>}
    {(run.state === "ready" || pager || onRetry) && <div className="card-actions">
      {pager}
      {run.state === "ready" && <button className="primary update-button" type="button" disabled={!connected || sending} onClick={onRun}><Icon name="download" />Update</button>}
      {onRetry && <button className="primary update-button" type="button" onClick={onRetry}><Icon name="refresh" />Try again</button>}
    </div>}
  </article>;
}

// trustLine says what the update checks: the release's SHA-256 for every
// release, and its publisher's signature for a signed one.
function trustLine(signing: string, publisher: string): string {
  if (signing === "signed" && publisher) return "Signed by " + publisher + ". Update installs it only with that signature and when each file matches the release's SHA-256";
  if (signing === "unsigned") return "Unsigned release. Update installs it only when each file matches the release's SHA-256";
  return "Update installs it only when each file matches the release's SHA-256, and a signed release only with its publisher's signature";
}
