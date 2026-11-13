import { useEffect, type ReactNode } from "react";
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
import { goblinName } from "./task-words";
import { SkipWithNote } from "./skip-with-note";
import { CardMail } from "./card-mail";
import { inlineElements, linkedText } from "./messageText";
import { copyValues } from "./CopyValue";
import { PathLink, pathLinks, type PathCard } from "./path-link";
import { TerminalBlock } from "./terminal-block";
import type { TerminalResult } from "./terminal-text";

// A command the CEO, or a goblin, needs the Overlord to run: who asks when it
// is a goblin's own, why, the shell, the exact text that runs and where, each
// local path it names a link that opens it, a message he sends himself with
// Copy, and one Run button that says where it runs, with Skip and the
// pencil beside it for a command he will not run: the pencil sends his answer
// in words instead. Run turns the card into the
// command's own terminal, in place of the notice it once showed: he types
// there and it completes by itself, Complete, or Failed with one line saying
// why and who takes the next step, keeping the end of what its terminal
// showed. The command, what it left and how it ended read in one terminal
// block, as a chat row's command does (#2524), never with its exit code,
// which History keeps. An administrator's
// command shows its terminal once he confirmed Windows' own prompt. The
// browser never sends the command; Run names the stored item.
export function RunCard({ run, goblin, connected, now, instance, sending, error, onRun, onSkip, onAnswer, pager }: {
  run: Run; goblin?: Task; connected: boolean; now: number; instance: string; sending: boolean; error: string; onRun: () => void; onSkip: () => void; onAnswer: (text: string) => void; pager?: ReactNode;
}) {
  const [feedback, showFeedback] = useClickFeedback();
  useEffect(() => { if (error) showFeedback(error); }, [error, showFeedback]);
  const running = run.state === "running";
  const mark = runMark(run);
  const failure = runFailure(run);
  const asker = run.task ? goblin ? goblinName(goblin) : run.task : "the CEO";
  const ending: TerminalResult | null = run.state === "failed" ? { tone: "error", text: "Failed", icon: mark.icon } : null;
  const card: PathCard = { kind: "run", id: run.id, instance };
  return <article className={"run-card" + (running && run.terminal ? " live" : "")} aria-labelledby={"run-" + run.id}>
    {run.task && <p className="asker"><Avatar persona={personaFor(goblin)} small /><span><strong>{asker}</strong> asks you to run this · {run.state === "ready" ? "waiting " + age(run.created_at, now).replace(/ ago$/, "") : "asked " + age(run.created_at, now)}</span></p>}
    <header className="run-head">
      <ConnectorMark mark={shellMark(run.shell)} label={shellLabel(run.shell)} />
      <span className="chip">{shellLabel(run.shell)}</span>
      {run.admin && <span className="chip admin"><Icon name="shield" />Admin</span>}
      <span className={"run-state delivery " + (mark.trouble ? "failed" : "succeeded")} role="status"><Icon name={mark.icon} />{failure ? "Failed" : mark.label}</span>
    </header>
    <h3 id={"run-" + run.id}>{inlineElements(run.title, copyValues, pathLinks(card))}</h3>
    {failure && <p className="run-failure">{linkedText(failure)}</p>}
    <TerminalBlock shell={run.shell} command={run.command} output={running ? "" : run.output} result={ending} />
    {run.cwd && <p className="run-cwd"><PathLink path={run.cwd} card={card} /></p>}
    {run.mail && <CardMail mail={run.mail} />}
    {run.reason && !mark.trouble && !mark.label.includes(run.reason) && <p className="muted">{linkedText(run.reason)}</p>}
    {running && (run.terminal
      ? <RunTerminal run={run} instance={instance} connected={connected} />
      : <p className="muted run-waiting" role="status"><Icon name={run.admin ? "shield" : "terminal"} />{run.admin ? "Confirm the Windows prompt to run it as administrator. It runs here once you do." : "Starting its terminal"}</p>)}
    {(run.state === "ready" || pager) && <div className="card-actions">
      <ClickFeedback text={feedback} />
      {pager}
      {run.state === "ready" && <>
        {run.admin && <small className="muted">Windows will ask to confirm.</small>}
        <SkipWithNote asker={asker} task={run.task} instance={instance} disabled={!connected || sending} onSkip={onSkip} onAnswer={onAnswer} />
        <button className="primary run-button" type="button" disabled={!connected || sending} onClick={onRun}><Icon name="play" />{runLabel(run.shell, run.admin)}</button>
      </>}
    </div>}
  </article>;
}
