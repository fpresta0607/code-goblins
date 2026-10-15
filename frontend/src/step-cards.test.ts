import test from "node:test";
import assert from "node:assert/strict";
import { canPressDone, closedLine, credentialAsk, credentialFinish, credentialHeading, credentialSettled, inputLabel, stillAsked } from "./credentials.ts";
import { settledIcon } from "./commandQueue.ts";
import { runMark } from "./feedback.ts";
import type { CredentialRequest, Run } from "./types.ts";

// A goblin's card for a step only he can take: a page, its steps, and the
// inputs he enters.
const card = (changes: Partial<CredentialRequest> = {}): CredentialRequest => ({
  id: "cred-0123456789abcdef", generation: "g", identity: "i", by: "goblin", task: "shop-signup",
  project: "acme-shop", names: [], plain: [], labels: {}, why: "Create the shop's test account", link: "https://example.com/signup", steps: ["Create the account", "Accept the terms"],
  window: false, window_state: "", received: [],
  env_file: "", existing: [], hints: [], services: {}, state: "open", saved: [], replaced: [], typed: [], fetched: [], written: [], reason: "",
  created_at: "2026-10-14T23:00:00Z", expires_at: "2026-10-15T23:00:00Z", closed_at: "", fetch: null, ...changes,
});

test("a card of clicks is headed by what it is for, and its line in the inbox says so too", () => {
  assert.equal(credentialHeading(card()), "Create the shop's test account");
  assert.equal(credentialAsk(card()), "Create the shop's test account");
});

test("a card that asks for plain values asks him to enter them, by their labels", () => {
  const email = card({ plain: ["ACCOUNT_EMAIL"], labels: { ACCOUNT_EMAIL: "The account's email" } });
  assert.equal(credentialHeading(email), "Enter the account's email for acme-shop");
  assert.equal(credentialAsk(email), "Enter the account's email for acme-shop");
  assert.equal(credentialHeading(card({ plain: ["ACCOUNT_EMAIL"], names: ["STRIPE_SECRET_KEY"] })), "Enter 2 values for acme-shop");
});

test("each input is shown by its label, or by its name when it has none", () => {
  const labelled = card({ plain: ["ACCOUNT_EMAIL"], names: ["STRIPE_SECRET_KEY"], labels: { ACCOUNT_EMAIL: "The account's email" } });
  assert.equal(inputLabel(labelled, "ACCOUNT_EMAIL"), "The account's email");
  assert.equal(inputLabel(labelled, "STRIPE_SECRET_KEY"), "STRIPE_SECRET_KEY");
});

test("a card asks for the inputs not in yet, secret and plain alike", () => {
  assert.deepEqual(stillAsked(card({ names: ["STRIPE_SECRET_KEY"], plain: ["ACCOUNT_EMAIL"], received: ["ACCOUNT_EMAIL"] })), ["STRIPE_SECRET_KEY"]);
  assert.deepEqual(stillAsked(card({ names: ["STRIPE_SECRET_KEY"], plain: ["ACCOUNT_EMAIL"], saved: ["STRIPE_SECRET_KEY"] })), ["ACCOUNT_EMAIL"]);
});

test("Done closes only a card that asks for no input: one that asks closes when its inputs arrive", () => {
  assert.equal(canPressDone(card()), true);
  assert.equal(canPressDone(card({ plain: ["ACCOUNT_EMAIL"] })), false);
  assert.equal(canPressDone(card({ names: ["STRIPE_SECRET_KEY"] })), false);
  assert.equal(canPressDone(card({ state: "done" })), false);
});

test("a closed card says how it closed, in his history", () => {
  assert.equal(credentialSettled(card({ state: "done" })), "Done");
  assert.equal(credentialSettled(card({ state: "dismissed" })), "Closed. Bring it back: cfo auth request --reopen cred-0123456789abcdef");
  assert.equal(credentialSettled(card({ state: "withdrawn", reason: "shop-signup is gone" })), "Withdrawn: shop-signup is gone");
  assert.equal(credentialSettled(card({ state: "saved", plain: ["ACCOUNT_EMAIL"], received: ["ACCOUNT_EMAIL"] })), "Sent ACCOUNT_EMAIL to shop-signup");
  assert.equal(credentialSettled(card({ state: "saved", names: ["STRIPE_SECRET_KEY"], saved: ["STRIPE_SECRET_KEY"], plain: ["ACCOUNT_EMAIL"], received: ["ACCOUNT_EMAIL"] })), "Saved STRIPE_SECRET_KEY for acme-shop and sent ACCOUNT_EMAIL to shop-signup");
});

test("a card that finishes on screen says how, and that its goblin goes on", () => {
  assert.deepEqual(credentialFinish(card({ state: "done" })), { heading: "Done", label: "Its goblin goes on now." });
  assert.deepEqual(credentialFinish(card({ state: "dismissed" })), { heading: "Closed", label: "Its goblin goes on without it. Bring it back: cfo auth request --reopen cred-0123456789abcdef" });
  assert.deepEqual(credentialFinish(card({ state: "saved", plain: ["ACCOUNT_EMAIL"], received: ["ACCOUNT_EMAIL"] })), { heading: "Sent", label: "Sent ACCOUNT_EMAIL to shop-signup" });
  assert.deepEqual(credentialFinish(card({ state: "saved", names: ["STRIPE_SECRET_KEY"], saved: ["STRIPE_SECRET_KEY"] })), { heading: "Stored", label: "Saved STRIPE_SECRET_KEY for acme-shop" });
  assert.equal(credentialFinish(card({ state: "open" })), null);
  assert.equal(credentialFinish(card({ state: "withdrawn" })), null);
});

// The Overlord, 2026-10-15: a card he closes leaves one short line with the
// command to give the value later, or to bring the card back.
test("a closed card's place says how to give its secret later, or to bring it back", () => {
  assert.equal(closedLine(card({ names: ["FLEET_GITHUB_TOKEN"], project: "code-goblins" })), "Give it later: cfo auth store --project code-goblins FLEET_GITHUB_TOKEN");
  assert.equal(closedLine(card({ names: ["A_KEY", "B_KEY"], saved: ["A_KEY"] })), "Give it later: cfo auth store --project acme-shop B_KEY");
  assert.equal(closedLine(card({ names: ["A_KEY", "B_KEY"] })), "Give them later: cfo auth store --project acme-shop A_KEY, then the same for B_KEY");
  assert.equal(closedLine(card({ plain: ["ACCOUNT_EMAIL"] })), "Bring it back: cfo auth request --reopen cred-0123456789abcdef");
  assert.equal(closedLine(card()), "Bring it back: cfo auth request --reopen cred-0123456789abcdef");
  assert.equal(credentialFinish(card({ state: "dismissed", task: "", names: ["FLEET_GITHUB_TOKEN"] }))?.label, "Give it later: cfo auth store --project acme-shop FLEET_GITHUB_TOKEN");
});

// The Overlord, 2026-10-15: Other is his free answer on any card.
test("a card he answered in words says so, and shows his answer in his history", () => {
  assert.deepEqual(credentialFinish(card({ state: "answered", reason: "Use the bot token" })), { heading: "Answered", label: "Its goblin has your answer." });
  assert.equal(credentialSettled(card({ state: "answered", reason: "Use the bot token" })), "You answered: Use the bot token");
  assert.equal(settledIcon({ kind: "credential", key: "credential:x", request: card({ state: "answered" }) }, []).icon, "check-double");
});

test("a command he skipped or answered in words reads Skipped or Answered, never Withdrawn", () => {
  const run = (reason: string) => ({ state: "withdrawn", task: "olive-task", reason } as Run);
  assert.equal(runMark(run("The Overlord skipped it from the Command Center.")).label, "Skipped");
  assert.equal(runMark(run("The Overlord answered instead: use the deploy key")).label, "Answered");
  assert.equal(runMark(run("olive-task is gone")).label, "Withdrawn");
});

test("a card closed as done shows the check of a finished item, and a dismissed one the close mark", () => {
  const item = (state: string) => ({ kind: "credential" as const, key: "credential:x", request: card({ state }) });
  assert.equal(settledIcon(item("done"), []).icon, "check-double");
  assert.equal(settledIcon(item("dismissed"), []).icon, "close");
});
