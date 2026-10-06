import { test } from "node:test";
import assert from "node:assert/strict";
import { comebackLine } from "./comeback.ts";
import type { Comeback, ComebackEntry } from "./types.ts";

const entry = (id: string, state: ComebackEntry["state"], reason = ""): ComebackEntry => ({ id, state, reason });
const comeback = (cfo: ComebackEntry | undefined, ...goblins: ComebackEntry[]): Comeback => ({ signed_in: "2026-10-06T22:00:00Z", ...(cfo ? { cfo } : {}), goblins });

test("a restart that brought nothing back says nothing", () => {
  assert.equal(comebackLine(undefined), null);
  assert.equal(comebackLine(comeback(undefined)), null);
});

test("while the fleet comes back the line says what is back and that the next waits for memory", () => {
  assert.deepEqual(comebackLine(comeback(entry("cfo", "back"), entry("a", "back"), entry("b", "back"), entry("c", "waiting"), entry("d", "waiting"), entry("e", "waiting"), entry("f", "waiting"))), {
    text: "Coming back after a restart: the CFO is back, 2 of 6 goblins are back.", detail: "The next one starts when memory allows.", isDone: false,
  });
  assert.deepEqual(comebackLine(comeback(entry("cfo", "waiting"), entry("a", "waiting"))), {
    text: "Coming back after a restart: the CFO comes back first, 0 of 1 goblin is back.", detail: "The next one starts when memory allows.", isDone: false,
  });
});

test("once everything is back the line says what resumed", () => {
  const goblins = ["a", "b", "c", "d", "e", "f"].map((id) => entry(id, "back"));
  assert.deepEqual(comebackLine(comeback(entry("cfo", "back"), ...goblins)), { text: "Resumed the CFO and 6 goblins after a restart.", detail: "", isDone: true });
  assert.deepEqual(comebackLine(comeback(undefined, entry("a", "back"))), { text: "Resumed 1 goblin after a restart.", detail: "", isDone: true });
  assert.deepEqual(comebackLine(comeback(entry("cfo", "back"))), { text: "Resumed the CFO after a restart.", detail: "", isDone: true });
});

test("a goblin or the CFO that did not come back is named, and its card or bar says why", () => {
  const five = ["a", "b", "c", "d", "e"].map((id) => entry(id, "back"));
  assert.deepEqual(comebackLine(comeback(entry("cfo", "back"), ...five, entry("cg-site-hero", "stopped", "Codex is not signed in"))), {
    text: "Resumed the CFO and 5 of 6 goblins after a restart.", detail: "cg-site-hero did not come back; its card says why.", isDone: true,
  });
  assert.deepEqual(comebackLine(comeback(undefined, entry("a", "stopped"), entry("b", "stopped"))), {
    text: "Resumed 0 of 2 goblins after a restart.", detail: "a and b did not come back; their cards say why.", isDone: true,
  });
  assert.deepEqual(comebackLine(comeback(entry("cfo", "stopped", "its conversation s-1 could not be resumed; Reopen on its bar starts it on a new one"), entry("a", "back"))), {
    text: "Resumed 1 goblin after a restart.", detail: "The CFO did not come back: its conversation s-1 could not be resumed; Reopen on its bar starts it on a new one.", isDone: true,
  });
});
