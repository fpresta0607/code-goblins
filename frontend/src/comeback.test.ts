import { test } from "node:test";
import assert from "node:assert/strict";
import { comebackLine } from "./comeback.ts";
import type { Comeback, ComebackEntry } from "./types.ts";

const entry = (id: string, state: ComebackEntry["state"], reason = ""): ComebackEntry => ({ id, state, reason });
const comeback = (ceo: ComebackEntry | undefined, ...goblins: ComebackEntry[]): Comeback => ({ signed_in: "2026-10-06T22:00:00Z", ...(ceo ? { ceo } : {}), goblins });

test("a restart that brought nothing back says nothing", () => {
  assert.equal(comebackLine(undefined), null);
  assert.equal(comebackLine(comeback(undefined)), null);
});

test("while the fleet comes back the line says what is back", () => {
  assert.deepEqual(comebackLine(comeback(entry("ceo", "back"), entry("a", "back"), entry("b", "back"), entry("c", "waiting"), entry("d", "waiting"), entry("e", "waiting"), entry("f", "waiting"))), {
    text: "Coming back after a restart: the CEO is back, 2 of 6 goblins are back.", isDone: false,
  });
  assert.deepEqual(comebackLine(comeback(entry("ceo", "waiting"), entry("a", "waiting"))), {
    text: "Coming back after a restart: the CEO comes back first, 0 of 1 goblin is back.", isDone: false,
  });
});

// The Overlord, 2026-10-08: "everything error wise goes to cfo". Why the CEO
// or a goblin did not come back is the CEO's to hear, not a line on the board.
test("a CEO that did not come back is named in the line without its reason", () => {
  assert.deepEqual(comebackLine(comeback(entry("ceo", "stopped", "its conversation s-1 could not be resumed."), entry("a", "waiting"))), {
    text: "Coming back after a restart: the CEO did not come back, 0 of 1 goblin is back.", isDone: false,
  });
  assert.deepEqual(comebackLine(comeback(entry("ceo", "stopped", "its conversation s-1 could not be resumed."), entry("a", "back"))), {
    text: "Resumed 1 goblin after a restart.", isDone: true,
  });
});

test("once everything is back the line says what resumed", () => {
  const goblins = ["a", "b", "c", "d", "e", "f"].map((id) => entry(id, "back"));
  assert.deepEqual(comebackLine(comeback(entry("ceo", "back"), ...goblins)), { text: "Resumed the CEO and 6 goblins after a restart.", isDone: true });
  assert.deepEqual(comebackLine(comeback(undefined, entry("a", "back"))), { text: "Resumed 1 goblin after a restart.", isDone: true });
  assert.deepEqual(comebackLine(comeback(entry("ceo", "back"))), { text: "Resumed the CEO after a restart.", isDone: true });
});

test("goblins that did not come back count against what resumed", () => {
  const five = ["a", "b", "c", "d", "e"].map((id) => entry(id, "back"));
  assert.deepEqual(comebackLine(comeback(entry("ceo", "back"), ...five, entry("cg-site-hero", "stopped", "Codex is not signed in"))), {
    text: "Resumed the CEO and 5 of 6 goblins after a restart.", isDone: true,
  });
  assert.deepEqual(comebackLine(comeback(undefined, entry("a", "stopped"), entry("b", "stopped"))), {
    text: "Resumed 0 of 2 goblins after a restart.", isDone: true,
  });
});
