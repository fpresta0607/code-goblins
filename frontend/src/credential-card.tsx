import { Fragment, useRef, useState, type ReactNode } from "react";
import { message, request as call } from "./api";
import { object, strings, type CredentialRequest, type Snapshot } from "./types";
import { Avatar } from "./Avatar";
import { Icon } from "./Icon";
import { age } from "./presentation";
import { personaFor } from "./workflow";
import { credentialHeading, credentialSettled, destination, linkLabel, onThisMachine, stillNeeded, storeCommand, terminalNames, valueWarnings } from "./credentials";
import { CredentialReplaceDialog } from "./credential-replace-dialog";
import "./credential-card.css";

// What the board said when it refused a save or a Run: why, and the names the
// scope holds that it wants him to confirm replacing. It names names only.
function refusal(error: unknown): { reason: string; existing: string[] } {
  const cause = error instanceof Error && typeof error.cause === "object" && error.cause !== null ? object(error.cause) : {};
  return { reason: message(error), existing: strings(cause.existing) };
}

// Who heard of a saved request: the goblins the board told to reload their
// credentials, or the terminal, which told them itself.
function toldLine(request: CredentialRequest): string {
  if (request.told.length === 1) return "1 running goblin was told to reload its credentials.";
  if (request.told.length > 1) return request.told.length + " running goblins were told to reload their credentials.";
  if (request.saved.length && request.saved.every((name) => request.typed.includes(name))) return "Typed in a terminal on this PC, which told the project's running goblins itself.";
  return "";
}

interface Confirm { names: string[]; action: "save" | "run"; confirmed: string[] }

// A request for credential values: who asks and why, one row per name saying
// where its value goes, a hidden field for each value still needed, and the
// cfo auth store lines with Copy and Run to type them in a terminal on this PC
// instead. Values are typed only on the board on this PC; a board opened from
// another machine shows the commands. A value stays in its field, never in
// the page's markup, until Save sends it once in the body of the save
// request; the board answers with names, and every field empties after a
// submit. A stored value is replaced only once he confirms it, from the card
// or for its terminal alike.
export function CredentialCard({ request, snapshot, connected, pager }: { request: CredentialRequest; snapshot: Snapshot; connected: boolean; pager?: ReactNode }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [round, setRound] = useState(0);
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const commandBox = useRef<HTMLPreElement>(null);
  const local = onThisMachine(window.location.hostname);
  const open = request.state === "open";
  const needed = stillNeeded(request);
  const held = (name: string) => request.existing.includes(name) && !request.saved.includes(name);
  const terminalOpen = (snapshot.runs || []).some((run) => run.credential_request === request.id && (run.state === "ready" || run.state === "running"));
  const task = snapshot.tasks.find((candidate) => candidate.id === request.task);
  const asker = request.task ? task?.title || request.task : "The CFO";
  const typeable = terminalNames(request, []);
  const commands = (typeable.length ? typeable : needed).map((name) => storeCommand(request.project, name));
  const filled = needed.filter((name) => !!values[name]);
  const headers = { "Content-Type": "application/json", "X-CFO-Token": snapshot.instance };
  // A new round mounts empty fields, so no value outlives its submit.
  const empty = () => { setValues({}); setRound((prior) => prior + 1); };
  const ask = (names: string[], action: Confirm["action"], confirmed: string[]) => setConfirm({ names, action, confirmed });

  const save = async (replace: string[]) => {
    const unconfirmed = filled.filter((name) => held(name) && !replace.includes(name));
    if (unconfirmed.length) return ask(unconfirmed, "save", replace);
    setBusy(true); setError("");
    try {
      await call("/api/credentials/save", undefined, { method: "POST", headers, body: JSON.stringify({ id: request.id, generation: request.generation, values: Object.fromEntries(filled.map((name) => [name, values[name]])), replace }) });
      empty();
    } catch (failure: unknown) {
      const { reason, existing } = refusal(failure);
      const unasked = existing.filter((name) => !replace.includes(name));
      if (unasked.length) ask(unasked, "save", replace);
      else { empty(); setError(reason); }
    } finally { setBusy(false); }
  };
  // Run opens a window on this PC for the names the scope does not hold, and
  // for a held one only once he confirms replacing it.
  const run = async (replace: string[]) => {
    if (!terminalNames(request, replace).length) return ask(needed.filter(held), "run", replace);
    setBusy(true); setError("");
    try {
      await call("/api/credentials/terminal", undefined, { method: "POST", headers, body: JSON.stringify({ id: request.id, generation: request.generation, replace }) });
      empty();
    } catch (failure: unknown) {
      const { reason, existing } = refusal(failure);
      const unasked = existing.filter((name) => !replace.includes(name));
      if (unasked.length) ask(unasked, "run", replace);
      else setError(reason);
    } finally { setBusy(false); }
  };
  // A page without the clipboard, such as a board opened over plain http,
  // selects the commands for Ctrl+C instead.
  const copy = () => {
    const box = commandBox.current;
    if (!window.isSecureContext && box) {
      window.getSelection()?.selectAllChildren(box);
      setError("This page cannot reach the clipboard; the commands are selected, so press Ctrl+C.");
      return;
    }
    navigator.clipboard.writeText(commands.join("\n")).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1400); }, () => setError("The browser did not copy the commands; select them and press Ctrl+C."));
  };

  const told = toldLine(request);
  return <article className="question-card credential-card" data-credential-request={request.id} aria-labelledby={"credential-" + request.id}>
    <p className="asker"><Avatar persona={request.task ? personaFor(task) : "cfo"} small />
      <span><strong>{asker}</strong> needs {request.names.length === 1 ? "1 credential" : request.names.length + " credentials"} · {open ? "waiting " + age(request.created_at).replace(/ ago$/, "") : (request.state === "saved" ? "saved " : "closed ") + age(request.closed_at || request.created_at)}</span>
    </p>
    <h3 id={"credential-" + request.id} tabIndex={-1}>{credentialHeading(request)}</h3>
    <p className="credential-promise">Each value is saved only where its row says. No goblin's chat, log or board record ever sees it.</p>
    {open && !local && <p className="credential-remote"><Icon name="lock" />Values can only be typed on the board on your PC. Here, copy the terminal command instead.</p>}
    {request.state === "saved" && <p className="credential-outcome delivery succeeded"><Icon name="check-double" /><span><strong>{credentialSettled(request)}.</strong>{told && <small>{told}</small>}</span></p>}
    {!open && request.state !== "saved" && <p className="credential-outcome warning-text"><Icon name="clock" /><span><strong>{request.reason}</strong></span></p>}
    {request.state === "saved" && request.reason && <p className="warning-text">{request.reason}</p>}
    {open && terminalOpen && <p className="credential-terminal-open" role="status"><Icon name="terminal" />A terminal is open on this PC: type each value there, where nothing you type is shown. Saving here waits until it closes.</p>}
    <div className="credential-table-wrap">
      <table className="credential-table">
        <thead><tr><th scope="col">Name</th><th scope="col">Saved to</th><th scope="col">Used for</th><th scope="col">Get it here</th><th scope="col">Value</th></tr></thead>
        <tbody>{request.names.map((name, index) => {
          const where = destination(request, name);
          const saved = request.saved.includes(name);
          const hint = request.hints.find((candidate) => candidate.name === name);
          return <tr key={name} data-credential-name={name} data-state={saved ? "saved" : held(name) ? "held" : "needed"}>
            <th scope="row"><code>{name}</code></th>
            <td className="credential-saved-to"><ul className="credential-where">
              {where.repository && <li><Icon name="folder" /><span>Repository {where.repository}</span></li>}
              <li><Icon name="key" /><span>Credential scope {where.scope}</span></li>
              <li><Icon name="terminal" /><span>{where.usedBy.join(" · ")}</span></li>
            </ul></td>
            {index === 0 && <td className="credential-why" rowSpan={request.names.length}>{request.why}</td>}
            {index === 0 && <td className="credential-link" rowSpan={request.names.length}>{request.link
              ? <a href={request.link} target="_blank" rel="noreferrer"><Icon name="external" /><span>{linkLabel(request.link).split("/").map((part, place, parts) => <Fragment key={place}>{part}{place < parts.length - 1 && <>/<wbr /></>}</Fragment>)}</span></a>
              : <span className="muted">No link given</span>}</td>}
            <td className="credential-value">{saved
              ? <span className="credential-saved"><Icon name="check" /><span><strong>{request.replaced.includes(name) ? "Replaced" : "Saved"}</strong><small>{request.typed.includes(name) ? "Typed in the terminal" : "Pasted on the card"}</small></span></span>
              : !open ? <span className="muted">Not stored</span>
              : !local ? <span className="credential-pill"><Icon name={held(name) ? "refresh" : "lock"} />{held(name) ? "Stored before: saving replaces it" : "Type it on your PC"}</span>
              : <>
                <input key={round} className="credential-input" type="password" autoComplete="new-password" spellCheck={false} autoCapitalize="off" autoCorrect="off" data-1p-ignore="" data-lpignore="true" aria-label={"Value for " + name}
                  onChange={(event) => { const value = event.currentTarget.value; setValues((prior) => ({ ...prior, [name]: value })); setError(""); }} />
                {held(name) && <small className="credential-note"><Icon name="refresh" />Stored before: saving replaces it</small>}
                {valueWarnings(hint, values[name] || "").map((warning) => <small key={warning} className="credential-note warning-text"><Icon name="warning" />{warning}</small>)}
              </>}
            </td>
          </tr>;
        })}</tbody>
      </table>
    </div>
    {open && <div className="credential-terminal">
      <Icon name="terminal" />
      <div className="credential-terminal-text">
        <p>{local ? "Or type " + (commands.length > 1 ? "them" : "it") + " in a terminal on this PC, where " + (commands.length > 1 ? "they stay" : "it stays") + " hidden:" : "Type " + (commands.length > 1 ? "them" : "it") + " in a terminal on your PC, where " + (commands.length > 1 ? "they stay" : "it stays") + " hidden:"}</p>
        <pre ref={commandBox}>{commands.map((line) => <code key={line}>{line}</code>)}</pre>
      </div>
      <div className="credential-terminal-actions">
        <button type="button" className="icon-button raised" aria-label="Copy the commands" data-tip={copied ? "Copied" : "Copy"} onClick={copy}><Icon name={copied ? "check" : "copy"} /></button>
        {local && <button type="button" className="icon-button raised" aria-label="Run in a terminal on this PC" data-tip="Run in a terminal on this PC" data-tip-align="end" disabled={!connected || busy || terminalOpen} onClick={() => void run([])}><Icon name="play" /></button>}
      </div>
    </div>}
    {error && <p className="warning-text" role="alert">{error}</p>}
    {(pager || (open && local)) && <div className="card-actions">
      {pager}
      {open && local && <button type="button" className="primary credential-save" disabled={!connected || busy || terminalOpen || filled.length === 0} onClick={() => void save([])}><Icon name="lock" />Save</button>}
    </div>}
    {confirm && <CredentialReplaceDialog names={confirm.names} project={request.project} action={confirm.action} onCancel={() => setConfirm(null)}
      onReplace={() => { const next = [...confirm.confirmed, ...confirm.names]; setConfirm(null); void (confirm.action === "save" ? save(next) : run(next)); }} />}
  </article>;
}
