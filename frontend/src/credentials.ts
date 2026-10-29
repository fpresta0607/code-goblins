import type { CredentialFetch, CredentialHint, CredentialRequest } from "./types.ts";

// Names as a sentence lists them: "A", "A and B", "A, B and C".
export const listNames = (names: string[]) => names.length < 2 ? names.join("") : names.slice(0, -1).join(", ") + " and " + names[names.length - 1];

// What became of a request: the secrets saved for its project and the plain
// values sent to its goblin, his Done or Dismiss, or why it closed.
export function credentialSettled(request: CredentialRequest): string {
  if (request.state === "granted") return "Granted " + request.grant_service + " to " + request.task + (request.reason ? ". " + request.reason : "");
  if (request.state === "dismissed" && request.grant_service) return "Denied " + request.grant_service + " for " + request.task;
  const sent = request.received.length ? "sent " + listNames(request.received) + " to " + (request.task || "the CFO") : "";
  if (request.state === "saved" && request.saved.length) return "Saved " + listNames(request.saved) + " for " + request.project + (sent ? " and " + sent : "");
  if (request.state === "saved") return sent.charAt(0).toUpperCase() + sent.slice(1);
  if (request.state === "done") return "Done";
  if (request.state === "dismissed") return "Closed. " + closedLine(request);
  if (request.state === "answered") return "You wrote: " + request.reason;
  if (request.state === "withdrawn") return "Withdrawn: " + request.reason;
  return request.reason;
}

// How a card that closed while he looked at it finishes: every input in, his
// Done, his Dismiss, or his words from Something else?, sent as a question's
// answer is. A card that closed any other way, or is open, does not
// finish this way.
export function credentialFinish(request: CredentialRequest): { heading: string; label: string } | null {
  if (request.state === "granted") return { heading: "Granted", label: credentialSettled(request) };
  if (request.state === "dismissed" && request.grant_service) return { heading: "Denied", label: credentialSettled(request) };
  if (request.state === "saved") return { heading: request.saved.length ? "Stored" : "Sent", label: credentialSettled(request) };
  if (request.state === "done") return { heading: "Done", label: request.task ? "Its goblin goes on now." : "" };
  if (request.state === "dismissed") return { heading: "Closed", label: (request.task ? "Its goblin goes on without it. " : "") + closedLine(request) };
  if (request.state === "answered") return { heading: "Sent", label: credentialSettled(request) };
  return null;
}

// The one line a card he closed leaves: the command to give its secrets
// later, or, for a card with none, to bring it back.
export function closedLine(request: CredentialRequest): string {
  const needed = stillNeeded(request);
  if (!needed.length) return "Bring it back: cfo auth request --reopen " + request.id;
  const first = storeCommand(request.project, needed[0]);
  return needed.length === 1 ? "Give it later: " + first : "Give them later: " + first + ", then the same for " + listNames(needed.slice(1));
}

// The secrets a request still asks for: those not saved yet, by the card or
// in its terminal.
export function stillNeeded(request: CredentialRequest): string[] {
  return request.names.filter((name) => !request.saved.includes(name));
}

// Every input a card still asks for: its secrets not saved and its plain
// values not sent.
export function stillAsked(request: CredentialRequest): string[] {
  return [...stillNeeded(request), ...request.plain.filter((name) => !request.received.includes(name))];
}

// An input as the card names it: its label, else its name.
export const inputLabel = (request: CredentialRequest, name: string) => request.labels[name] || name;

// An input as a sentence names it: its label from lower case, else its name.
const inSentence = (request: CredentialRequest, name: string) => { const label = request.labels[name]; return label ? label.charAt(0).toLowerCase() + label.slice(1) : name; };

// Done closes an open card that asks for no input: one that asks closes once
// its inputs arrive.
export const canPressDone = (request: CredentialRequest) => request.state === "open" && !request.grant_service && !request.names.length && !request.plain.length;

// The inputs a request asks for: those still asked while it waits, and all
// of them once nothing is.
const asked = (request: CredentialRequest) => { const open = stillAsked(request); return open.length ? open : [...request.names, ...request.plain]; };

// What a request asks in one line, for the inbox, the CFO bar and alerts: a
// card of clicks by what it is for.
export function credentialAsk(request: CredentialRequest): string {
  if (request.grant_service) return "Grant " + request.grant_service + " to " + request.task;
  if (!request.names.length && !request.plain.length) return request.why;
  const names = asked(request).map((name) => inSentence(request, name));
  return (request.plain.length ? "Enter " : "Paste ") + listNames(names) + " for " + request.project;
}

// The card's heading: a single input by its label or name, several by
// count, and a card of clicks by what it is for. It stays the same while he
// fills row after row.
export function credentialHeading(request: CredentialRequest): string {
  if (request.grant_service) return credentialAsk(request);
  if (!request.names.length && !request.plain.length) return request.why;
  const inputs = [...request.names, ...request.plain];
  if (!request.plain.length) return "Paste " + (inputs.length > 1 ? inputs.length + " credentials" : inputs[0]) + " for " + request.project;
  return "Enter " + (inputs.length > 1 ? inputs.length + " values" : inSentence(request, inputs[0])) + " for " + request.project;
}

// A step of the card's header as he reads it, starting with a capital.
export const stepText = (step: string) => step.trim().charAt(0).toUpperCase() + step.trim().slice(1);

// A request's link as its step shows it: the site and page, without the scheme.
export function linkLabel(link: string): string {
  const url = new URL(link);
  return url.host + url.pathname.replace(/\/+$/, "");
}

// The names the terminal types: each one not saved yet that the scope does
// not hold, and a held one only once he confirms replacing it, as a save from
// the card does (CFO decision 3624).
export function terminalNames(request: CredentialRequest, replace: string[]): string[] {
  return stillNeeded(request).filter((name) => !request.existing.includes(name) || replace.includes(name));
}

// The exact terminal fallback for one name, as cfo prints it: a scope a shell
// would split is quoted.
export function storeCommand(project: string, name: string): string {
  const scope = /^[A-Za-z0-9._/:-]+$/.test(project) ? project : '"' + project + '"';
  return "cfo auth store --project " + scope + " " + name;
}

// A format hint's warnings for a value: one that starts none of the expected
// ways, and the advice for a start the hint names. A hint never blocks a save.
export function valueWarnings(hint: CredentialHint | undefined, value: string): string[] {
  if (!hint || !value) return [];
  const warnings: string[] = [];
  if (hint.prefixes.length && !hint.prefixes.some((prefix) => value.startsWith(prefix))) warnings.push("Expected to start with " + hint.prefixes.join(" or "));
  for (const warning of hint.warn) if (value.startsWith(warning.prefix)) warnings.push(warning.say);
  return warnings;
}

// Where a card's values go, in a muted line above its rows: the project's
// vault, which its goblins read, and the env file they are also set in.
export function whereItGoes(request: CredentialRequest): string {
  return "Goes to the " + request.project + " vault for its goblins" + (request.env_file ? ", and into " + request.env_file : "");
}

// The auth.json services that declare a row's name and so read its value too,
// in a muted line under the name.
export function readersOf(request: CredentialRequest, name: string): string {
  const services = request.services[name] || [];
  return services.length ? "Also read by the " + listNames(services) + (services.length > 1 ? " services" : " service") : "";
}

// How a stored row's value was stored, in muted words after its Stored.
export function storedHow(request: CredentialRequest, name: string): string {
  const how = request.fetched.includes(name) ? "fetched from the page" : request.typed.includes(name) ? "typed in the terminal" : "pasted on the card";
  return how + (request.written.includes(name) ? ", set in " + request.env_file : "");
}

// What a card's Fetch it for me offers: the keys a reviewed page reads, with
// the project's login, straight into the vault.
export function fetchOffer(fetch: CredentialFetch): string {
  const what = fetch.names.length === 2 ? "both keys" : fetch.names.length === 1 ? fetch.names[0] : `the ${fetch.names.length} keys`;
  const page = fetch.names.every((name) => name.split("_").includes("TEST")) ? "test key page" : "key page";
  return `The fleet can read ${what} from ${fetch.provider}'s ${page} with the ${fetch.login} login, straight into the vault. No agent sees ${fetch.names.length === 1 ? "it" : "them"}.`;
}

// The board takes values only on this PC: a board opened over the tailnet
// shows the card without its value fields.
export function onThisMachine(hostname: string): boolean {
  return hostname === "127.0.0.1" || hostname === "localhost" || hostname === "[::1]";
}
