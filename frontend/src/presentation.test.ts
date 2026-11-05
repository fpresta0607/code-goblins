import assert from "node:assert/strict";
import test from "node:test";
import { age, rowAge } from "./presentation.ts";

const NOW = Date.parse("2026-11-05T12:00:00Z");
const before = (milliseconds: number) => new Date(NOW - milliseconds).toISOString();
const SECOND = 1000, MINUTE = 60 * SECOND, HOUR = 60 * MINUTE, DAY = 24 * HOUR;

test("age says how long ago in whole minutes or longer, never in seconds", () => {
  const cases: [string, string, string][] = [
    ["this moment", before(0), "under a minute ago"],
    ["sixteen seconds", before(16 * SECOND), "under a minute ago"],
    ["the last second of the first minute", before(MINUTE - 1), "under a minute ago"],
    ["a whole minute", before(MINUTE), "1m ago"],
    ["two minutes and a half", before(2 * MINUTE + 30 * SECOND), "2m ago"],
    ["the last minute of the first hour", before(HOUR - 1), "59m ago"],
    ["a whole hour", before(HOUR), "1h ago"],
    ["the last hour of the first day", before(DAY - 1), "23h ago"],
    ["a whole day", before(DAY), "1d ago"],
    ["a time past the clock's last step", before(-30 * SECOND), "under a minute ago"],
  ];
  for (const [name, timestamp, want] of cases) assert.equal(age(timestamp, NOW), want, name);
});

test("age reads the clock it is given, not the time it is drawn", () => {
  // Arrange
  const asked = before(90 * SECOND);

  // Act
  const atThisStep = age(asked, NOW);
  const atTheNextStep = age(asked, NOW + MINUTE);

  // Assert
  assert.equal(atThisStep, "1m ago");
  assert.equal(atTheNextStep, "2m ago");
});

test("age says so of a time it cannot back", () => {
  const cases: [string, string, string][] = [
    ["no time", "", "No evidence"],
    ["Go's zero time", "0001-01-01T00:00:00Z", "No evidence"],
    ["not a time", "not a time", "Unknown freshness"],
  ];
  for (const [name, timestamp, want] of cases) assert.equal(age(timestamp, NOW), want, name);
});

test("rowAge says the first minute in the room of a narrow column, and everything else as age does", () => {
  const cases: [string, string, string][] = [
    ["this moment", before(0), "just now"],
    ["the last second of the first minute", before(MINUTE - 1), "just now"],
    ["a whole minute", before(MINUTE), "1m ago"],
    ["a whole day", before(DAY), "1d ago"],
    ["no time", "", "No evidence"],
    ["not a time", "not a time", "Unknown freshness"],
  ];
  for (const [name, timestamp, want] of cases) assert.equal(rowAge(timestamp, NOW), want, name);
});
