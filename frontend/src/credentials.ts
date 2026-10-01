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

// The card's heading: a single credential by name, several by count.
export function credentialHeading(request: CredentialRequest): string {
  const names = asked(request);
  return names.length > 1 ? "Paste " + names.length + " credentials for " + request.project : credentialAsk(request);
}

// A request's link as its row shows it: the site and page, without the scheme.
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

// Where a row's value goes: the repository the request is for (empty when it
// has no checkout on this machine), the credential scope, and who reads it:
// every goblin of the project, through its auth.ps1, and each auth.json
// service that declares the name.
export function destination(request: CredentialRequest, name: string): { repository: string; scope: string; usedBy: string[] } {
  const repository = request.repository.split(/[\\/]/).filter(Boolean).pop() || "";
  return { repository, scope: request.project, usedBy: ["Goblins' auth.ps1", ...(request.services[name] || []).map((service) => service + " service")] };
}

// The board takes values only on this PC: a board opened over the tailnet
// shows the card without its value fields.
export function onThisMachine(hostname: string): boolean {
  return hostname === "127.0.0.1" || hostname === "localhost" || hostname === "[::1]";
}
