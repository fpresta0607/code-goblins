import test from "node:test";
import assert from "node:assert/strict";
import { credentialAsk, credentialHeading, credentialSettled, destination, linkLabel, onThisMachine, stillNeeded, storeCommand, terminalNames, valueWarnings } from "./credentials.ts";
import { boardAlerts, outlived } from "./alertRules.ts";
import { cfoSummary } from "./cfoSummary.ts";
import { waitingItems } from "./commandQueue.ts";
import type { CredentialRequest, Run, Snapshot, Task } from "./types.ts";

const request = (changes: Partial<CredentialRequest> = {}): CredentialRequest => ({
  id: "cred-0123456789abcdef", generation: "g", identity: "i", by: "goblin", task: "add-billing",
  project: "precisiondocs", repository: "C:\\dev\\precisiondocs", names: ["STRIPE_SECRET_KEY", "DATABASE_URL"], why: "Charge test cards", link: "",
  env_file: "", existing: [], hints: [], services: {}, state: "open", saved: [], replaced: [], typed: [], written: [], told: [], reason: "",
  created_at: "2026-10-01T03:00:00Z", expires_at: "2026-10-02T03:00:00Z", closed_at: "", ...changes,
});

const board = (credentials: CredentialRequest[], runs: Run[] = []): Snapshot =>
  ({ tasks: [{ id: "add-billing", title: "Add Stripe billing", phase: "working", generation: "g1" } as Task], questions: [], reviews: [], runs, credentials, attention: [] as string[] }) as unknown as Snapshot;

test("a settled request says which names were saved for which project, or why it closed", () => {
  assert.equal(credentialSettled(request({ state: "saved", saved: ["STRIPE_SECRET_KEY", "DATABASE_URL"] })), "Saved STRIPE_SECRET_KEY and DATABASE_URL for precisiondocs");
  assert.equal(credentialSettled(request({ state: "saved", saved: ["STRIPE_SECRET_KEY"] })), "Saved STRIPE_SECRET_KEY for precisiondocs");
  assert.equal(credentialSettled(request({ state: "expired", reason: "Nobody saved it within 24 hours." })), "Nobody saved it within 24 hours.");
});

test("the card asks only for the names not saved yet", () => {
  assert.deepEqual(stillNeeded(request({ saved: ["DATABASE_URL"] })), ["STRIPE_SECRET_KEY"]);
  assert.deepEqual(stillNeeded(request()), ["STRIPE_SECRET_KEY", "DATABASE_URL"]);
});

test("a request says what it asks in one line, naming its names and its project", () => {
  assert.equal(credentialAsk(request({ names: ["STRIPE_SECRET_KEY"] })), "Paste STRIPE_SECRET_KEY for precisiondocs");
  assert.equal(credentialAsk(request()), "Paste STRIPE_SECRET_KEY and DATABASE_URL for precisiondocs");
  assert.equal(credentialAsk(request({ names: ["A", "B", "C"] })), "Paste A, B and C for precisiondocs");
  assert.equal(credentialAsk(request({ saved: ["STRIPE_SECRET_KEY"] })), "Paste DATABASE_URL for precisiondocs");
});

test("the card's heading names a single credential and counts several, and keeps what it asked once it closes", () => {
  assert.equal(credentialHeading(request({ names: ["STRIPE_SECRET_KEY"] })), "Paste STRIPE_SECRET_KEY for precisiondocs");
  assert.equal(credentialHeading(request()), "Paste 2 credentials for precisiondocs");
  assert.equal(credentialHeading(request({ state: "saved", saved: ["STRIPE_SECRET_KEY", "DATABASE_URL"] })), "Paste 2 credentials for precisiondocs");
});

test("a request's link shows its site and page, without the scheme", () => {
  assert.equal(linkLabel("https://dashboard.stripe.com/test/apikeys"), "dashboard.stripe.com/test/apikeys");
  assert.equal(linkLabel("https://supabase.com/dashboard/"), "supabase.com/dashboard");
  assert.equal(linkLabel("https://resend.com"), "resend.com");
});

// CFO decision 3624: Run and the card behave the same. A stored name is typed
// again only when he confirms replacing it.
test("the terminal types the names the scope does not hold, and a stored one only once he confirms replacing it", () => {
  const held = request({ existing: ["DATABASE_URL"] });
  assert.deepEqual(terminalNames(held, []), ["STRIPE_SECRET_KEY"]);
  assert.deepEqual(terminalNames(held, ["DATABASE_URL"]), ["STRIPE_SECRET_KEY", "DATABASE_URL"]);
  assert.deepEqual(terminalNames(request({ existing: ["DATABASE_URL"], saved: ["STRIPE_SECRET_KEY"] }), []), []);
  assert.deepEqual(terminalNames(request({ saved: ["DATABASE_URL"] }), []), ["STRIPE_SECRET_KEY"]);
});

test("the terminal command is the exact cfo auth store line, quoting a scope a shell would split", () => {
  assert.equal(storeCommand("precisiondocs", "STRIPE_SECRET_KEY"), "cfo auth store --project precisiondocs STRIPE_SECRET_KEY");
  assert.equal(storeCommand("Acme Shop (old)", "STRIPE_SECRET_KEY"), 'cfo auth store --project "Acme Shop (old)" STRIPE_SECRET_KEY');
});

test("a format hint warns about a value of the wrong kind and advises on one it names, never blocking", () => {
  const hint = { name: "STRIPE_SECRET_KEY", prefixes: ["sk_", "rk_"], warn: [{ prefix: "sk_live_", say: "Use a restricted rk_live_ key." }] };
  assert.deepEqual(valueWarnings(hint, ""), []);
  assert.deepEqual(valueWarnings(hint, "rk" + "_test_abc"), []);
  assert.deepEqual(valueWarnings(hint, "pk" + "_test_abc"), ["Expected to start with sk_ or rk_"]);
  assert.deepEqual(valueWarnings(hint, "sk" + "_live_abc"), ["Use a restricted rk_live_ key."]);
  assert.deepEqual(valueWarnings(undefined, "anything"), []);
});

test("each row says where its value goes: the repository, the credential scope, the env file it is also set in, and who reads it", () => {
  const where = destination(request({ services: { STRIPE_SECRET_KEY: ["stripe", "billing"] } }), "STRIPE_SECRET_KEY");
  assert.deepEqual(where, { repository: "precisiondocs", scope: "precisiondocs", file: "", usedBy: ["Goblins' auth.ps1", "stripe service", "billing service"] });
  assert.deepEqual(destination(request({ repository: "" }), "DATABASE_URL"), { repository: "", scope: "precisiondocs", file: "", usedBy: ["Goblins' auth.ps1"] });
  assert.deepEqual(destination(request({ env_file: ".env.docker.local" }), "DATABASE_URL"), { repository: "precisiondocs", scope: "precisiondocs", file: ".env.docker.local", usedBy: ["Goblins' auth.ps1"] });
});

test("values are typed only on the board on this PC", () => {
  for (const host of ["127.0.0.1", "localhost", "[::1]"]) assert.equal(onThisMachine(host), true, host);
  for (const host of ["sermon.tailcc4238.ts.net", "100.101.102.103", "goblins.local", "board.localhost"]) assert.equal(onThisMachine(host), false, host);
});

test("a new request alerts once, naming who asks and what, and opens its card", () => {
  const filed = request({ names: ["STRIPE_SECRET_KEY"] });
  const alerts = boardAlerts(board([]), board([filed]));
  assert.deepEqual(alerts.map(({ key, tone, speaker, text, action, task, target }) => ({ key, tone, speaker, text, action, task, target })), [
    { key: "credential:" + filed.id, tone: "needs", speaker: "Add Stripe billing", text: "Add Stripe billing asks: Paste STRIPE_SECRET_KEY for precisiondocs", action: "Open Command Center", task: "add-billing", target: { kind: "command", key: "credential:" + filed.id } },
  ]);
  const [cfo] = boardAlerts(board([]), board([{ ...filed, by: "cfo", task: "" }]));
  assert.equal(cfo.speaker, "CFO");
  assert.equal(cfo.text, "The CFO asks: Paste STRIPE_SECRET_KEY for precisiondocs");
  assert.deepEqual(boardAlerts(board([filed]), board([filed])), [], "a request already there alerts nothing");
});

test("a request's alert has nothing left to open once the request is saved or expired", () => {
  const filed = request({ names: ["STRIPE_SECRET_KEY"] });
  const [alert] = boardAlerts(board([]), board([filed]));
  assert.equal(outlived(alert, board([filed])), false, "an open request still has its card");
  assert.equal(outlived(alert, board([{ ...filed, state: "saved", saved: ["STRIPE_SECRET_KEY"] }])), true);
  assert.equal(outlived(alert, board([{ ...filed, state: "expired" }])), true);
  assert.equal(outlived(alert, board([])), false, "a request the snapshot does not hold is not known to be closed");
});

test("the CFO bar says what a request waits for", () => {
  assert.deepEqual(cfoSummary(board([request({ names: ["STRIPE_SECRET_KEY"] })])), { asking: true, line: "Waiting on you: Paste STRIPE_SECRET_KEY for precisiondocs" });
  assert.equal(cfoSummary(board([request({ state: "saved", saved: ["STRIPE_SECRET_KEY"] })])).asking, false);
});

test("a request waits on the Overlord until it closes, and its terminal is part of its card, not an item of its own", () => {
  const filed = request({ names: ["STRIPE_SECRET_KEY"] });
  const terminal = { id: "credential-1", title: "Type STRIPE_SECRET_KEY", state: "running", created_at: "2026-10-01T03:01:00Z", credential_request: filed.id } as Run;
  assert.deepEqual(waitingItems(board([filed], [terminal])).map((item) => item.key), ["credential:" + filed.id]);
  assert.deepEqual(waitingItems(board([{ ...filed, state: "saved" }])), []);
  assert.deepEqual(waitingItems(board([{ ...filed, state: "expired" }])), []);
});
