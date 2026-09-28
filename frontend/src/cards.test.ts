import { test } from "node:test";
import assert from "node:assert/strict";
import { clockText, panelViews, showMoreLabel } from "./cards.ts";
import type { Session, Task } from "./types.ts";

const MINUTE = 60_000;
const now = Date.parse("2026-09-27T12:00:00Z");
const ago = (minutes: number) => new Date(now - minutes * MINUTE).toISOString();

test("a session clock reads in whole minutes, hours and days", () => {
  assert.equal(clockText(ago(0.5), now, "running"), "just started");
  assert.equal(clockText(ago(0.5), now, "waiting"), "just queued");
  assert.equal(clockText(ago(1), now, "running"), "1m");
  assert.equal(clockText(ago(47), now, "running"), "47m");
  assert.equal(clockText(ago(60), now, "running"), "1h 0m");
  assert.equal(clockText(ago(134), now, "running"), "2h 14m");
  assert.equal(clockText(ago(24 * 60), now, "waiting"), "1d 0h");
  assert.equal(clockText(ago(27 * 60 + 59), now, "waiting"), "1d 3h");
});

test("a clock without a start shows nothing rather than a guess", () => {
  assert.equal(clockText("", now, "running"), "");
  assert.equal(clockText("0001-01-01T00:00:00Z", now, "running"), "");
  assert.equal(clockText("not a time", now, "waiting"), "");
  assert.equal(clockText(new Date(now + 5 * MINUTE).toISOString(), now, "running"), "just started", "a start a little ahead of this clock is not negative time");
});

test("a description offers Show more only while it is cut off, and Show less once opened", () => {
  assert.equal(showMoreLabel(false, true), "Show more");
  assert.equal(showMoreLabel(true, true), "Show less");
  assert.equal(showMoreLabel(true, false), "Show less");
  assert.equal(showMoreLabel(false, false), "");
});

test("a queued task with no terminal has a Task view only; a started one and the CFO also have a Terminal", () => {
  const task = (generation: string) => ({ id: "t", generation }) as Task;
  assert.deepEqual(panelViews(task(""), undefined), ["task"]);
  assert.deepEqual(panelViews(task("s1"), undefined), ["task", "terminal"]);
  assert.deepEqual(panelViews(undefined, undefined), ["task", "terminal"], "the CFO");
  assert.deepEqual(panelViews(task(""), { id: "child" } as Session), ["task", "terminal"], "a reported child session has its own terminal view");
});

test("pausing or stopping switches an open terminal back to task details", () => {
  for (const phase of ["pausing", "paused", "stopping", "stopped"]) {
    assert.deepEqual(panelViews({ id: "t", generation: "s1", phase } as Task), ["task"]);
  }
});
