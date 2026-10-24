import { test } from "node:test";
import assert from "node:assert/strict";
import { cardDate, clockText, fullDate, panelViews, showMoreLabel } from "./cards.ts";
import type { Session, Task } from "./types.ts";
import { parseSnapshot } from "./types.ts";
import { sessionEnd } from "./session-end.ts";

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

const weekday = (date: Date) => date.toLocaleDateString(undefined, { weekday: "short" });
const monthDay = (date: Date) => date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
for (const [name, at, want] of [
  ["earlier today shows its time", new Date(2026, 8, 27, 9, 5), new Date(2026, 8, 27, 9, 5).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })],
  ["yesterday shows its weekday", new Date(2026, 8, 26, 23, 59), weekday(new Date(2026, 8, 26))],
  ["six days ago shows its weekday", new Date(2026, 8, 21, 8, 0), weekday(new Date(2026, 8, 21))],
  ["a resume in three days shows its weekday", new Date(2026, 8, 30, 9, 0), weekday(new Date(2026, 8, 30))],
  ["a week ago shows its month and day", new Date(2026, 8, 20, 20, 0), monthDay(new Date(2026, 8, 20))],
  ["last year shows its month and day", new Date(2025, 9, 14, 19, 22), monthDay(new Date(2025, 9, 14))],
] as const) {
  test(`a card's date ${name}`, () => {
    // Arrange
    const today = new Date(2026, 8, 27, 18, 0).getTime();

    // Act
    const shown = cardDate(at, today);

    // Assert
    assert.equal(shown, want);
  });
}

test("a card date's tip names its whole date and time", () => {
  // Arrange
  const at = new Date(2026, 9, 14, 19, 58);
  const fields: Intl.DateTimeFormatOptions[] = [{ weekday: "short" }, { year: "numeric" }, { month: "short" }, { day: "numeric" }, { hour: "numeric" }, { minute: "2-digit" }];

  // Act
  const tip = fullDate(at);

  // Assert
  for (const field of fields) {
    const [type] = Object.keys(field);
    const part = new Intl.DateTimeFormat(undefined, field).formatToParts(at).find((each) => each.type === type);
    assert.ok(part, `${type} formats on its own`);
    assert.ok(tip.includes(part.value), `${tip} names its ${type}, ${part.value}`);
  }
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

test("a session whose messages the board reads offers Messages beside its terminal, and never without one", () => {
  const task = (generation: string, phase = "working") => ({ id: "t", generation, phase }) as Task;
  assert.deepEqual(panelViews(task("s1"), undefined, true), ["messages", "task", "terminal"]);
  assert.deepEqual(panelViews(undefined, undefined, true), ["messages", "task", "terminal"], "the CFO");
  assert.deepEqual(panelViews(task(""), undefined, true), ["task"], "a queued task has no terminal yet");
  assert.deepEqual(panelViews(task("s1", "pausing"), undefined, true), ["task"]);
  assert.deepEqual(panelViews(task("s1"), undefined, false), ["task", "terminal"]);
});

test("pausing or stopping switches an open terminal back to task details", () => {
  for (const phase of ["pausing", "stopping"]) {
    assert.deepEqual(panelViews({ id: "t", generation: "s1", phase } as Task), ["task"]);
  }
});

test("ended sessions retain a Terminal view for their summary", () => {
  for (const phase of ["paused", "stopped"]) {
    const task = parseSnapshot({ healthy: true, tasks: [{ id: "t", generation: "s1", phase, verified: false }] }).tasks[0];
    assert.deepEqual(panelViews(task), ["task", "terminal"]);
  }
  const history = parseSnapshot({ healthy: true, tasks: ["finished:t", "merged:https://example.com/pull/1"].map((id) => ({ id, archived: true, phase: "done", verified: false })) }).tasks;
  assert.deepEqual(panelViews(history[0]), ["task", "terminal"]);
  assert.deepEqual(panelViews(history[1]), ["task"]);
});

test("cleanup retirement stays distinct from an explicit lifecycle stop", () => {
  const task = parseSnapshot({ healthy: true, tasks: [{ id: "finished:t", archived: true, phase: "stopped", retired_at: "2026-09-30T12:00:00Z", verified: false }] }).tasks[0];
  assert.equal(sessionEnd(task), "retired");
  assert.equal(sessionEnd({ ...task, lifecycle: { phase: "stopped", action: "stop", at: "2026-09-30T12:00:00Z", kept: [], stopped: [], problems: [], handoff_saved: false, validation_restarts: false } }), "stopped");
});
