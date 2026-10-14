import type { CredentialHint, CredentialRequest } from "./types.ts";

// Names as a sentence lists them: "A", "A and B", "A, B and C".
export const listNames = (names: string[]) => names.length < 2 ? names.join("") : names.slice(0, -1).join(", ") + " and " + names[names.length - 1];

// What became of a request: the names saved for its project, or why it closed.
export function credentialSettled(request: CredentialRequest): string {
  if (request.state === "saved") return "Saved " + listNames(request.saved) + " for " + request.project;
  return request.reason;
}

// The names a request still asks for: those not saved yet, by the card or in
// its terminal.
export function stillNeeded(request: CredentialRequest): string[] {
  return request.names.filter((name) => !request.saved.includes(name));
}

// The names a request asks for: those still needed while it waits, and all
// of them once nothing is.
const asked = (request: CredentialRequest) => { const needed = stillNeeded(request); return needed.length ? needed : request.names; };

// What a request asks in one line, for the inbox, the CFO bar and alerts.
export function credentialAsk(request: CredentialRequest): string {
  return "Paste " + listNames(asked(request)) + " for " + request.project;
}

// The card's heading: a single credential by name, several by count. It
// stays the same while he stores row after row.
export function credentialHeading(request: CredentialRequest): string {
  return "Paste " + (request.names.length > 1 ? request.names.length + " credentials" : request.names[0]) + " for " + request.project;
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

// Where a row's value goes, for the tip on its empty mark: the project's
// vault, which its goblins and each auth.json service that declares the name
// read, and the env file it is also set in.
export function whereItGoes(request: CredentialRequest, name: string): string {
  const services = request.services[name] || [];
  const readers = services.length ? " and the " + listNames(services) + (services.length > 1 ? " services" : " service") : "";
  return "Goes to the " + request.project + " vault for its goblins" + readers + (request.env_file ? ", and into " + request.env_file : "");
}

// How a stored row's value was stored, for the tip on its check.
export function storedTip(request: CredentialRequest, name: string): string {
  const how = request.fetched.includes(name) ? "fetched from the page" : request.typed.includes(name) ? "typed in the terminal" : "pasted on the card";
  return (request.replaced.includes(name) ? "Replaced" : "Stored") + " in the " + request.project + " vault, " + how +
    (request.written.includes(name) ? ", and set in " + request.env_file : "");
}

// The tip on the check of a name the vault held before the request.
export function heldTip(request: CredentialRequest): string {
  return "Already in the " + request.project + " vault";
}

// The board takes values only on this PC: a board opened over the tailnet
// shows the card without its value fields.
export function onThisMachine(hostname: string): boolean {
  return hostname === "127.0.0.1" || hostname === "localhost" || hostname === "[::1]";
}
