import { Fragment, useRef, useState, type ReactNode } from "react";
import { message, request as call } from "./api";
import { object, strings, type CredentialRequest, type Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { personaFor } from "./workflow";
import { credentialHeading, fetchOffer, heldTip, linkLabel, onThisMachine, stepText, stillNeeded, storeCommand, storedTip, terminalNames, valueWarnings, whereItGoes } from "./credentials";
import { CredentialField } from "./credential-field";
import { CredentialReplaceDialog } from "./credential-replace-dialog";
import { RunTerminal } from "./run-terminal";
import "./credential-card.css";
import { ClickFeedback, useClickFeedback } from "./click-feedback";
import { goblinName } from "./task-words";

// The names the scope holds that the board wants him to confirm replacing,
// from its refusal of a save or a Run. It names names only.
function heldNames(error: unknown): string[] {
  const cause = error instanceof Error && typeof error.cause === "object" && error.cause !== null ? object(error.cause) : {};
  return strings(cause.existing);
}

// Text as a cell shows it: a long one wraps after each separator, never
// inside a word.
const wrappable = (text: string, separator: string) => text.split(separator).map((part, place, parts) => <Fragment key={place}>{part}{place < parts.length - 1 && <>{separator}<wbr /></>}</Fragment>);

interface Confirm { names: string[]; action: "save" | "run"; confirmed: string[] }

// A request for credential values: its header says how to get them, as the
// page to open and the asker's steps, numbered in order, and its table has a Name and a Value
// column, a field to paste each value into and a check once the value is in
// the project's vault. A pasted value is stored at once, unless its format
// hint warns about it; a typed one when he presses Enter or the row's lock.
// Where a value goes and how it was stored are in the tips. Below the table
// the cfo auth store lines have Copy and Run to type them in a terminal on
// this PC instead. Values are typed only on the board on this PC; a board
// opened from another machine shows the commands. A value is kept by the
// card, never in its field or the page's markup, until its row's save sends
// it once; the board answers with names, and the row's field empties after
// every answer. A stored value is replaced only once he confirms it, from the
// card or for its terminal alike. Once every value is stored the Command
// Center says so and moves on by itself.
export function CredentialCard({ request, snapshot, connected, pager }: { request: CredentialRequest; snapshot: Snapshot; connected: boolean; pager?: ReactNode }) {
  const [feedback, showFeedback] = useClickFeedback();
  const [values, setValues] = useState<Record<string, string>>({});
  // A row's round mounts its field again empty, so no value outlives its save.
  const [rounds, setRounds] = useState<Record<string, number>>({});
  const [storing, setStoring] = useState<string[]>([]);
  // The names the board answered as stored, before its snapshot says so.
  const [answered, setAnswered] = useState<string[]>([]);
  // Why the board refused a row's value, shown under its field until he
  // pastes again.
  const [refusals, setRefusals] = useState<Record<string, string>>({});
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  // His click on Fetch it for me, until the board's answer comes back.
  const [isFetching, setIsFetching] = useState(false);
  const commandBox = useRef<HTMLPreElement>(null);
  const local = onThisMachine(window.location.hostname);
  const open = request.state === "open";
  const saved = (name: string) => request.saved.includes(name) || answered.includes(name);
  const needed = stillNeeded(request).filter((name) => !answered.includes(name));
  const held = (name: string) => request.existing.includes(name) && !saved(name);
  const terminalOpen = (snapshot.runs || []).some((run) => run.credential_request === request.id && (run.state === "ready" || run.state === "running"));
  // The terminal runs on this card, on the board on his PC alone.
  const terminal = local ? (snapshot.runs || []).find((run) => run.credential_request === request.id && run.state === "running" && run.terminal) : undefined;
  const task = snapshot.tasks.find((candidate) => candidate.id === request.task);
  const asker = request.task ? task ? goblinName(task) : request.task : "The CFO";
  const typeable = terminalNames(request, []).filter((name) => !answered.includes(name));
  const commands = (typeable.length ? typeable : needed).map((name) => storeCommand(request.project, name));
  const headers = { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance };
  const empty = (name: string) => {
    setValues((prior) => { const rest = { ...prior }; delete rest[name]; return rest; });
    setRounds((prior) => ({ ...prior, [name]: (prior[name] || 0) + 1 }));
  };
  const ask = (names: string[], action: Confirm["action"], confirmed: string[]) => setConfirm({ names, action, confirmed });
  const warnings = (name: string, value: string) => valueWarnings(request.hints.find((hint) => hint.name === name), value);

  // store sends one row's value, the one pasted or the one its field holds.
  const store = async (name: string, replace: string[], pasted?: string) => {
    const value = pasted ?? values[name];
    if (!value || storing.includes(name)) return;
    if (held(name) && !replace.includes(name)) return ask([name], "save", replace);
    setStoring((prior) => [...prior, name]); showFeedback(""); setRefusals((prior) => ({ ...prior, [name]: "" }));
    try {
      const answer = object(await call("/api/credentials/save", undefined, { method: "POST", headers, body: JSON.stringify({ id: request.id, generation: request.generation, values: { [name]: value }, replace }) }));
      setAnswered((prior) => [...prior, ...strings(answer.saved)]);
      empty(name);
    } catch (failure: unknown) {
      if (heldNames(failure).includes(name) && !replace.includes(name)) ask([name], "save", replace);
      else { empty(name); setRefusals((prior) => ({ ...prior, [name]: message(failure) })); }
    } finally { setStoring((prior) => prior.filter((candidate) => candidate !== name)); }
  };
  // Run opens a window on this PC for the names the scope does not hold, and
  // for a held one only once he confirms replacing it.
  const run = async (replace: string[]) => {
    if (!terminalNames(request, replace).length) return ask(needed.filter(held), "run", replace);
    setBusy(true); showFeedback("");
    try {
      await call("/api/credentials/terminal", undefined, { method: "POST", headers, body: JSON.stringify({ id: request.id, generation: request.generation, replace }) });
      for (const name of request.names) empty(name);
    } catch (failure: unknown) {
      const existing = heldNames(failure);
      const unasked = existing.filter((name) => !replace.includes(name));
      if (unasked.length) ask(unasked, "run", replace);
      else showFeedback(message(failure));
    } finally { setBusy(false); }
  };
  // Fetch asks the board to read the keys a reviewed page shows into the
  // vault; the card shows the fetch running and how it ended.
  const fetchKeys = async () => {
    setIsFetching(true); showFeedback("");
    try { await call("/api/credentials/fetch", undefined, { method: "POST", headers, body: JSON.stringify({ id: request.id, generation: request.generation }) }); }
    catch (failure: unknown) { showFeedback(message(failure)); }
    finally { setIsFetching(false); }
  };
  const offer = open && local && request.fetch && request.fetch.names.some((name) => needed.includes(name)) ? request.fetch : null;
  // A page without the clipboard, such as a board opened over plain http,
  // selects the commands for Ctrl+C instead, and says so beside his click.
  const copy = () => {
    const box = commandBox.current;
    if (!window.isSecureContext && box) {
      window.getSelection()?.selectAllChildren(box);
      showFeedback("The commands are selected. Press Ctrl+C.");
      return;
    }
    navigator.clipboard.writeText(commands.join("\n")).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1400); }, () => showFeedback("Not copied. Select the commands and press Ctrl+C."));
  };

  const several = commands.length > 1;
  return <article className="question-card credential-card" data-credential-request={request.id} aria-labelledby={"credential-" + request.id}>
    <p className="asker"><Avatar persona={request.task ? personaFor(task) : "cfo"} small />
      <span><strong>{asker}</strong> {open ? "asks · waiting " + age(request.created_at).replace(/ ago$/, "") : "asked · closed " + age(request.closed_at || request.created_at)}</span>
    </p>
    <header className="credential-heading">
      <h3 id={"credential-" + request.id} tabIndex={-1}>{credentialHeading(request)}</h3>
      {request.why && <p className="credential-why">{request.why}</p>}
      {(request.link || request.steps.length > 0) && <ol className="credential-steps" aria-label="How to get them">
        {request.link && <li>Open <a href={request.link} target="_blank" rel="noreferrer"><span>{wrappable(linkLabel(request.link), "/")}</span><Icon name="external" /></a></li>}
        {request.steps.map((step, index) => <li key={index}>{stepText(step)}</li>)}
      </ol>}
    </header>
    {open && !local && <p className="credential-remote"><Icon name="lock" />Values can only be typed on the board on your PC. Here, copy the terminal command instead.</p>}
    {!open && <p className="credential-outcome" data-tip={request.reason}><Icon name="clock" /><strong>Closed</strong></p>}
    {open && terminalOpen && <p className="credential-terminal-open" role="status"><Icon name="terminal" />{!local ? "A terminal is open on the board on your PC: type each value there, where nothing you type is shown." : terminal ? "Type each value in the terminal below, where nothing you type is shown." : "A terminal is open on this PC: type each value there, where nothing you type is shown."}</p>}
    {open && terminal && <RunTerminal run={terminal} instance={snapshot.instance} connected={connected} />}
    {offer && <div className="credential-fetch">
      <p>{fetchOffer(offer)}</p>
      {offer.state === "fetching"
        ? <p className="credential-fetching" role="status"><Icon name="refresh" />Fetching from {offer.provider}'s page</p>
        : <button type="button" className="primary" disabled={!connected || isFetching || terminalOpen} onClick={() => void fetchKeys()}><Icon name="download" />Fetch it for me</button>}
      {offer.reason && offer.state !== "fetching" && <span className="credential-fetch-stopped" data-tip={offer.reason}><Icon name="warning" />Fetch stopped</span>}
    </div>}
    <table className="credential-table">
      <colgroup><col className="credential-name-column" /><col /></colgroup>
      <thead><tr><th scope="col">Name</th><th scope="col">Value</th></tr></thead>
      <tbody>{request.names.map((name) => {
        const value = values[name] || "";
        const isStoring = storing.includes(name);
        const state = saved(name) ? "saved" : held(name) ? "held" : "needed";
        return <tr key={name} data-credential-name={name} data-state={state}>
          <th scope="row"><code>{wrappable(name, "_")}</code></th>
          <td className="credential-value">{state === "saved"
            ? <span className="credential-stored" data-tip={storedTip(request, name)}><span className="credential-check" aria-hidden="true"><Icon name="check" /></span>Stored</span>
            : <div className="credential-value-line">
              {!open ? <span className="muted">Not stored</span>
                : !local ? <span className="credential-pill"><Icon name={held(name) ? "refresh" : "lock"} />{held(name) ? "Stored before: saving replaces it" : "Type it on your PC"}</span>
                : <CredentialField key={rounds[name] || 0} label={"Value for " + name} placeholder={held(name) ? "Paste a new one to replace it" : "Paste here"} disabled={!connected || terminalOpen || isStoring}
                  onValue={(next, pasted) => { setValues((prior) => ({ ...prior, [name]: next })); setRefusals((prior) => ({ ...prior, [name]: "" })); if (pasted && !warnings(name, next).length) void store(name, [], next); }}
                  onCommit={() => void store(name, [])} />}
              {open && local && warnings(name, value).map((warning) => <span key={warning} className="credential-warning" role="img" aria-label={warning} data-tip={warning}><Icon name="warning" /></span>)}
              {open && local && value && !isStoring && !terminalOpen
                ? <button type="button" className="icon-button raised credential-store" aria-label={"Store " + name} data-tip="Store it" disabled={!connected} onClick={() => void store(name, [])}><Icon name="lock" /></button>
                : state === "held"
                ? <span className="credential-check held" role="img" aria-label="Stored before" data-tip={heldTip(request)}><Icon name="check" /></span>
                : <span className={"credential-ring" + (isStoring ? " storing" : "")} role="img" aria-label={isStoring ? "Storing" : "Not stored yet"} data-tip={whereItGoes(request, name)} />}
            </div>}
            {refusals[name] && <p className="credential-refusal" role="alert"><Icon name="warning" />{refusals[name]}</p>}
          </td>
        </tr>;
      })}</tbody>
    </table>
    {open && needed.length > 0 && <div className="credential-terminal">
      <Icon name="terminal" />
      <div className="credential-terminal-text">
        <p>{local ? "Or type " + (several ? "them" : "it") + " in a terminal on this PC, where " + (several ? "they stay" : "it stays") + " hidden:" : "Type " + (several ? "them" : "it") + " in a terminal on your PC, where " + (several ? "they stay" : "it stays") + " hidden:"}</p>
        <pre ref={commandBox}>{commands.map((line) => <code key={line}>{line}</code>)}</pre>
      </div>
      <div className="credential-terminal-actions">
        <button type="button" className="icon-button raised" aria-label="Copy the commands" data-tip={copied ? "Copied" : "Copy"} onClick={copy}><Icon name={copied ? "check" : "copy"} /></button>
        {local && <button type="button" className="icon-button raised" aria-label="Run in a terminal on this PC" data-tip="Run in a terminal on this PC" disabled={!connected || busy || terminalOpen} onClick={() => void run([])}><Icon name="play" /></button>}
      </div>
    </div>}
    {(pager || feedback) && <div className="card-actions">
      <ClickFeedback text={feedback} />
      {pager}
    </div>}
    {confirm && <CredentialReplaceDialog names={confirm.names} project={request.project} action={confirm.action} onCancel={() => setConfirm(null)}
      onReplace={() => { const next = [...confirm.confirmed, ...confirm.names]; setConfirm(null); void (confirm.action === "save" ? store(confirm.names[0], next) : run(next)); }} />}
  </article>;
}
