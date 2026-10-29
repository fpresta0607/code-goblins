import assert from "node:assert/strict";
import test from "node:test";
import { boardAlerts, outlived } from "./alertRules.ts";
import { cfoSummary } from "./cfoSummary.ts";
import { closedAt, isOpen, itemFor, nextOpenKey, openKeys, settledIcon, settledItems, settledLabel, waitingItems } from "./commandQueue.ts";
import { holdClosed, remembered } from "./item-state.ts";
import { parseSnapshot } from "./types.ts";

const SIGN_IN = {
  id: "signin-stripe", generation: "generation-one", login: "stripe-account", provider: "Stripe", account: "PrecisionDocs account",
  project: "PrecisionDocs-AI", task: "billing", state: "ready", reason: "", created_at: "2026-10-12T10:00:00Z",
  expires_at: "2026-10-13T10:00:00Z", closed_at: "",
};
const board = (changes: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, instance: "board", revision: 1, browser_signins: [SIGN_IN], ...changes });

test("snapshots parse browser sign-ins and default a missing list to empty", () => {
  assert.deepEqual(parseSnapshot({ healthy: true, browser_signins: [SIGN_IN] }).browser_signins, [SIGN_IN]);
  assert.deepEqual(parseSnapshot({ healthy: true }).browser_signins, []);
  assert.deepEqual(parseSnapshot({ healthy: true, browser_signins: null }).browser_signins, []);
  assert.throws(() => parseSnapshot({ healthy: true, browser_signins: [{}] }));
  assert.throws(() => parseSnapshot({ healthy: true, browser_signins: [{ ...SIGN_IN, account: 123 }] }));
  assert.throws(() => parseSnapshot({ healthy: true, browser_signins: [{ ...SIGN_IN, state: "unknown" }] }));
});

for (const state of ["ready", "opening", "open", "signed_in", "expired"]) {
  test(`a ${state} sign-in has the right queue, count, history and outcome`, () => {
    const isWaiting = ["ready", "opening", "open"].includes(state);
    const snapshot = board({ browser_signins: [{ ...SIGN_IN, state, reason: state === "expired" ? "The request expired" : "", closed_at: isWaiting ? "" : "2026-10-12T11:00:00Z" }] });

    const item = itemFor(snapshot, "browser-signin:signin-stripe");

    assert.ok(item);
    assert.equal(isOpen(item), isWaiting);
    assert.equal(waitingItems(snapshot).length, Number(isWaiting));
    assert.equal(openKeys(snapshot).size, Number(isWaiting));
    assert.equal(cfoSummary(snapshot).waiting, Number(isWaiting));
    assert.equal(settledItems(snapshot).length, Number(!isWaiting));
    if (!isWaiting) {
      assert.equal(closedAt(item), "2026-10-12T11:00:00Z");
      assert.equal(settledIcon(item, []).icon, state === "signed_in" ? "check-double" : "close");
      assert.match(settledLabel(item, []), state === "signed_in" ? /Signed in.*Stripe.*PrecisionDocs account/ : /expired/i);
    }
  });
}

test("sign-ins join task ordering and advance to the next waiting card", () => {
  const snapshot = board({ attention: ["billing"], credentials: [{ id: "keys", task: "billing", state: "open", created_at: "2026-10-12T10:01:00Z" }] });
  const stack = waitingItems(snapshot);
  assert.deepEqual(stack.map((item) => item.key), ["browser-signin:signin-stripe", "credential:keys"]);
  assert.equal(nextOpenKey(stack, "browser-signin:signin-stripe"), "credential:keys");
});

test("sign-in arrivals alert once and their closed notifications expire", () => {
  const empty = board({ browser_signins: [] });
  const ready = board();
  const alerts = boardAlerts(empty, ready);
  assert.equal(alerts.length, 1);
  assert.equal(alerts[0].item, "browser-signin:signin-stripe");
  assert.match(alerts[0].text, /Sign in to Stripe for PrecisionDocs-AI/);
  assert.deepEqual(boardAlerts(ready, board({ browser_signins: [{ ...SIGN_IN, state: "open" }] })), []);
  assert.equal(outlived(alerts[0], board({ browser_signins: [{ ...SIGN_IN, state: "signed_in" }] })), true);
});

for (const state of ["signed_in", "expired"]) {
  test(`a ${state} sign-in stays closed through stale snapshots but a new publication opens`, () => {
    const memory = remembered(new Map(), board({ browser_signins: [{ ...SIGN_IN, state }] }));
    const held = holdClosed(board(), memory, new Map());
    assert.equal(held.browser_signins?.[0].state, state);
    assert.deepEqual(waitingItems(held), []);
    const next = board({ browser_signins: [{ ...SIGN_IN, generation: "generation-two", created_at: "2026-10-12T12:00:00Z" }] });
    assert.equal(holdClosed(next, memory, new Map()), next);
  });
}
