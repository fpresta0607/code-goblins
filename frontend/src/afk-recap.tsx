import { Avatar } from "./Avatar";
import { Icon, type IconName } from "./Icon";
import { AfkIconButton } from "./afk-icon-button";
import { AfkRecapGroup } from "./afk-recap-group";
import { AfkRecapRow } from "./afk-recap-row";
import { askedAndAnswered, byRepository, decisionSays, heldSays, inGroup, notLanded, safeLink, settled, shownUnder, stillWaiting, type AfkDecision, type AfkReport, type AfkSection } from "./afk";
import { AfkSpent } from "./afk-spent";
import { personaFor, pullRequestLabel } from "./workflow";
import type { Task } from "./types";
import "./afk.css";
import "./lantern.css";

// The mark each kind of line wears in the recap.
const MARKS: Record<string, IconName> = { merge: "merge", deploy: "external", migration: "database", install: "download", other: "check", pause: "pause", strike: "close", left: "comment", his: "warning" };

// AfkRecap is the recap's three parts under its headline: Needs you first and
// only when something does, then Done and Decided for you.
export function AfkRecap({ report, tasks, now, onGoThrough, onChange }: { report: AfkReport; tasks: Task[]; now: number; onGoThrough: (keys: string[]) => void; onChange: (key: string) => void }) {
  const goblin = (task: string) => tasks.find((one) => one.id === task)?.goblin_name || task;
  const avatar = (task: string) => <Avatar persona={task ? personaFor(tasks.find((one) => one.id === task)) : "cfo"} small />;
  const merged = new Set(inGroup(report, "done").flatMap((section) => section.entries.filter((entry) => entry.kind === "merge" && !notLanded(entry)).map((entry) => entry.what)));
  // Each pull request a goblin finished, once, by who finished it: those that
  // merged say so on their Merged line, and the rest are listed after.
  const finishedBy = new Map(report.finished.map((finish) => [finish.pr, finish]));
  const alsoFinished = [...finishedBy.values()].filter((finish) => !merged.has(finish.pr));
  const spent = report.spent.filter(shownUnder);
  const done = inGroup(report, "done");
  const settledItems = settled(report.held);
  // A pull request is named by its number on its repository's line, a link,
  // and what stood behind each is under the line's one Evidence.
  const pulls = (urls: string[], says: (url: string) => string, mark: IconName) => byRepository(urls).map((line) =>
    <AfkRecapRow key={mark + line.repository} mark={<Icon name={mark} />} what={line.repository} now={now} evidence={line.pulls.map((pull) => pull.number + " " + says(pull.url)).join("\n")}>
      {line.pulls.length > 0 && <span className="afk-pulls">{line.pulls.map((pull) => <a key={pull.url} href={safeLink(pull.url)} target="_blank" rel="noreferrer">{pull.number}</a>)}</span>}
    </AfkRecapRow>);
  const by = (url: string) => { const finish = finishedBy.get(url); return finish ? "by " + goblin(finish.task) : ""; };
  const doneRows = (section: AfkSection) => {
    if (section.entries.every((entry) => entry.kind === "merge" && !notLanded(entry) && !entry.struck)) {
      const basis = new Map(section.entries.map((entry) => [entry.what, decisionSays(entry).basis]));
      return pulls(section.entries.map((entry) => entry.what), (url) => [by(url), basis.get(url)].filter(Boolean).join(": "), "merge");
    }
    return section.entries.map((entry) => {
      const says = decisionSays(entry);
      const what = entry.kind === "pause" ? goblin(entry.task) + " paused " + entry.what : entry.kind === "merge" && /\/pull\/\d+/.test(entry.what) ? pullRequestLabel(entry.what) : entry.what;
      if (notLanded(entry)) return <AfkRecapRow key={entry.at + entry.what} mark={<Icon name="warning" />} what={what} link={says.href} detail={says.happened || "Not merged."} tone="warning" at={entry.at} now={now} evidence={says.basis} />;
      return <AfkRecapRow key={entry.at + entry.kind + entry.what} mark={<Icon name={entry.struck ? "close" : entry.settled ? "check" : MARKS[entry.kind] || "check"} />} what={what} link={entry.link} detail={entry.settled ? "Settled by the CFO: " + entry.settled : entry.kind === "pause" ? entry.outcome : ""}
        at={entry.at} now={now} evidence={says.basis} struck={!!entry.struck} />;
    });
  };
  const decided = inGroup(report, "decided");
  const needs = inGroup(report, "needs");
  const waiting = stillWaiting(report.held);
  const change = (entry: AfkDecision) => entry.item ? <AfkIconButton icon="edit" label="Change answer" onClick={() => onChange(entry.item)} /> : undefined;
  return <>
    {(needs.length > 0 || waiting.length > 0) && <section className="afk-part needs" aria-label="Needs you">
      <h3>Needs you</h3>
      {waiting.length > 0 && <button className="primary go-through" onClick={() => onGoThrough(waiting.map((one) => one.item))}>Open Command Center</button>}
      {waiting.length > 0 && <p className="afk-part-note">{waiting.length === 1 ? "1 thing waits" : waiting.length + " things wait"} for you in the Command Center, one card each.</p>}
      {needs.length > 0 && <ul className="afk-rows needs">
        {needs.flatMap((section) => section.entries).map((entry) => <AfkRecapRow key={entry.at + entry.what} mark={<Icon name="warning" />} what={entry.what} link={entry.link} tone="warning" at={entry.at} now={now} evidence={entry.evidence} />)}
      </ul>}
    </section>}
    {(done.length > 0 || alsoFinished.length > 0 || spent.length > 0 || report.disk || settledItems.length > 0) && <section className="afk-part" aria-label="Done">
      <h3>Done</h3>
      {done.filter((section) => !section.entries.every((entry) => entry.struck)).map((section) => <AfkRecapGroup key={section.title} title={section.title} count={section.entries.length}>{doneRows(section)}</AfkRecapGroup>)}
      {alsoFinished.length > 0 && <AfkRecapGroup title="Also finished" count={alsoFinished.length}>{pulls(alsoFinished.map((finish) => finish.pr), by, "pull-request")}</AfkRecapGroup>}
      {(spent.length > 0 || report.disk) && <section className="afk-group" aria-label="Spent">
        <h4>Spent <span className="afk-spent-legend"><i className="before" />Before AFK <i className="used" />While AFK <i className="left" />Left</span></h4>
        <AfkSpent spent={spent} disk={report.disk} />
      </section>}
      {settledItems.length > 0 && <AfkRecapGroup title="Settled" count={settledItems.length} folded>{settledItems.map((one) =>
        <AfkRecapRow key={one.item + one.at} mark={avatar(one.task)} what={one.what} detail={heldSays(one)} tone="answered" at={one.at} now={now} />)}</AfkRecapGroup>}
      {done.filter((section) => section.entries.every((entry) => entry.struck)).map((section) => <AfkRecapGroup key={section.title} title={section.title} count={section.entries.length} folded>{doneRows(section)}</AfkRecapGroup>)}
    </section>}
    {decided.length > 0 && <section className="afk-part" aria-label="Decided for you">
      <h3>Decided for you</h3>
      <p className="afk-part-note">The CFO answered each with its recommendation and acted on it. Change any of them here or in History.</p>
      {decided.map((section) => <AfkRecapGroup key={section.title} title={section.title} count={section.entries.length}>{section.entries.map((entry) => {
        if (entry.kind === "answer") {
          const { question, answer } = askedAndAnswered(entry.evidence);
          return <AfkRecapRow key={entry.at + entry.what} mark={avatar(entry.task)} what={question} link={entry.link} detail={"The CFO answered " + goblin(entry.task) + ": " + answer} tone="answered" at={entry.at} now={now} evidence={decisionSays(entry).basis} action={change(entry)} />;
        }
        return <AfkRecapRow key={entry.at + entry.what} mark={avatar("")} what={entry.what} detail={"The CFO answered: " + entry.answer} tone="answered" at={entry.at} now={now} evidence={decisionSays(entry).basis} action={change(entry)} />;
      })}</AfkRecapGroup>)}
    </section>}
    {report.notes.length > 0 && <section className="afk-part" aria-label="Not read">
      <h3>Not read</h3>
      <ul className="afk-rows">{report.notes.map((note) => <li key={note} className="afk-row note">{note}</li>)}</ul>
    </section>}
  </>;
}
