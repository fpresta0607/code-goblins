import assert from "node:assert/strict";
import test from "node:test";
import { ago, forgetDetail, loginDetail, loginName, parseBrowserLogins, type BrowserLogin } from "./browser-logins.ts";

const now = Date.parse("2026-10-13T20:00:00Z");
const login = (changes: Partial<BrowserLogin> = {}): BrowserLogin => ({
  id: "stripe-precisiondocs", provider: "Stripe", account: "PrecisionDocs", projects: ["PrecisionDocs-AI"],
  signedIn: true, signedInAt: "2026-10-13T18:00:00Z", usedAt: "2026-10-13T19:55:00Z", signsOutAfterFetch: false, inUse: false, ...changes,
});

test("a login's row names it and says when he signed in and a fetch used it, in the approved words", () => {
  assert.equal(loginName(login()), "Stripe · PrecisionDocs");
  assert.equal(loginDetail(login(), now), "For PrecisionDocs-AI. You signed in 2 hours ago, a fetch used it 5 minutes ago.");
  assert.equal(loginDetail(login({ usedAt: "" }), now), "For PrecisionDocs-AI. You signed in 2 hours ago.");
  assert.equal(loginDetail(login({ projects: ["SIQshift", "PrecisionDocs-AI"], signedIn: false, signedInAt: "", signsOutAfterFetch: true, usedAt: "2026-10-10T20:00:00Z" }), now),
    "For SIQshift and PrecisionDocs-AI. It signs out after each fetch, so each fetch brings the sign-in card. Last used 3 days ago.");
  assert.equal(loginDetail(login({ signedIn: false, signedInAt: "", usedAt: "" }), now), "For PrecisionDocs-AI. You are signed out, so the next fetch brings the sign-in card.");
});

test("forgetting a login says what goes and what stays", () => {
  assert.equal(forgetDetail(login()), "Stripe · PrecisionDocs. Its browser profile is deleted, so the next fetch for PrecisionDocs-AI asks you to sign in again. Your Stripe account and its keys are not touched.");
});

test("times read as a person says them", () => {
  assert.equal(ago("", now), "");
  assert.equal(ago("not a time", now), "");
  assert.equal(ago("2026-10-13T19:59:30Z", now), "just now");
  assert.equal(ago("2026-10-13T19:59:00Z", now), "1 minute ago");
  assert.equal(ago("2026-10-13T19:00:00Z", now), "1 hour ago");
  assert.equal(ago("2026-10-12T20:00:00Z", now), "1 day ago");
  assert.equal(ago("2026-10-13T21:00:00Z", now), "just now");
});

test("the board's logins are read field by field and default to signed out and idle", () => {
  const [parsed] = parseBrowserLogins([{ id: "stripe-siqstack", provider: "Stripe", account: "SIQstack", projects: ["SIQshift"], signed_in: true, signed_in_at: "2026-10-13T18:00:00Z", signs_out_after_fetch: true, in_use: true }]);
  assert.deepEqual(parsed, { id: "stripe-siqstack", provider: "Stripe", account: "SIQstack", projects: ["SIQshift"], signedIn: true, signedInAt: "2026-10-13T18:00:00Z", usedAt: "", signsOutAfterFetch: true, inUse: true });
  assert.deepEqual(parseBrowserLogins([{ id: "x" }])[0], { id: "x", provider: "", account: "", projects: [], signedIn: false, signedInAt: "", usedAt: "", signsOutAfterFetch: false, inUse: false });
  assert.deepEqual(parseBrowserLogins(undefined), []);
});
