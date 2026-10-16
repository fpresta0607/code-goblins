import { test } from "node:test";
import assert from "node:assert/strict";
import { AFK_OFF, AWAY_MS, afkLine, afkTime, offerFor, switchedBy, type Occasion } from "./afk.ts";
import { parseSnapshot, type Afk, type AfkHeld } from "./types.ts";

const NOW = Date.parse("2026-10-02T12:31:00Z");
const held = (changes: Partial<AfkHeld> = {}): AfkHeld => ({ item: "question:drop-legacy-invoices", task: "", what: "Migration 0042 drops legacy_invoices. Apply it?", at: "2026-10-02T03:05:00Z", waiting: true, now: "still waiting on you", meanwhile: "", recommendation: "", settled: "", ...changes });
const afk = (changes: Partial<Afk> = {}): Afk => ({ state: "on", since: "2026-10-02T02:10:00Z", from: "his own board (goblins-window.exe pid 4242)", asked: "", decided: 0, held: [], page: "", ...changes });
// AFK mode as the CFO turned it on at his ask.
const ASKED = "I'm stepping away, turn AFK on";
const BY_THE_CFO = { from: "the CFO at his ask (claude pid 4242)", asked: ASKED };
const snapshot = (value: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, instance: "fixture", ...value });

test("a supervisor from before AFK mode reached the board reads as off, and one that sends it is read whole", () => {
  assert.deepEqual(snapshot().afk, AFK_OFF);
  assert.deepEqual(snapshot({ afk: { state: "on", since: "2026-10-02T02:10:00Z", from: "his own terminal (powershell.exe pid 4242)", decided: 2, held: [{ item: "run:restart-db", what: "Restart the dev database", at: "2026-10-02T04:00:00Z", waiting: false, now: "succeeded" }, { item: "question:drop-legacy-invoices", what: "Apply it?", at: "2026-10-02T04:01:00Z", waiting: true, now: "still waiting on you", recommendation: "Keep it held" }] } }).afk,
    { state: "on", since: "2026-10-02T02:10:00Z", from: "his own terminal (powershell.exe pid 4242)", asked: "", decided: 2, held: [
      { item: "run:restart-db", task: "", what: "Restart the dev database", at: "2026-10-02T04:00:00Z", waiting: false, now: "succeeded", meanwhile: "", recommendation: "", settled: "" },
      { item: "question:drop-legacy-invoices", task: "", what: "Apply it?", at: "2026-10-02T04:01:00Z", waiting: true, now: "still waiting on you", meanwhile: "", recommendation: "Keep it held", settled: "" },
    ], page: "" });
  assert.equal(snapshot({ afk: { state: "on", since: "2026-10-02T02:10:00Z", ...BY_THE_CFO } }).afk.asked, ASKED);
  assert.equal(snapshot({ afk: { state: "off", page: "http://127.0.0.1:4387/session/afk" } }).afk.page, "http://127.0.0.1:4387/session/afk");
});

test("a state the board does not know stops the board rather than read as off", () => {
  assert.throws(() => snapshot({ afk: { state: "away" } }), /Invalid AFK state/);
  assert.throws(() => snapshot({ afk: { state: "on", held: [{ item: "question:q", waiting: "yes" }] } }), /Invalid response boolean/);
});

test("the bar says since when AFK mode is on, who turned it on and how much the CFO decided, and nothing of what is held, since nothing is held for him", () => {
  const cases: [string, Afk, string][] = [
    ["off says nothing", afk({ state: "off", since: "" }), ""],
    ["on with nothing yet", afk(), "AFK since 2:10 AM, from your board. 0 decided."],
    ["on by the CFO at his ask, without his words, which the offer and his page of the stretch quote", afk({ ...BY_THE_CFO, decided: 1 }), "AFK since 2:10 AM, turned on by the CFO at your ask. 1 decided."],
    ["on from his terminal, whatever waits", afk({ from: "his own terminal (powershell.exe pid 5151)", decided: 4, held: [held(), held({ item: "run:restart-db", waiting: false, now: "succeeded" }), held({ item: "review:waiting-task-1-7", task: "task-1" })] }), "AFK since 2:10 AM, from your terminal. 4 decided."],
    ["on with no word of where", afk({ from: "" }), "AFK since 2:10 AM. 0 decided."],
    ["a switch that cannot be read is not taken for on", afk({ state: "unreadable", since: "" }), ""],
  ];
  for (const [name, value, want] of cases) assert.equal(afkLine(value, NOW, "UTC", "en-US"), want, name);
});

test("a time is the time of day, with its day when that is not today", () => {
  assert.equal(afkTime("2026-10-02T02:10:00Z", NOW, "UTC", "en-US"), "2:10 AM");
  assert.equal(afkTime("2026-10-01T22:05:00Z", NOW, "UTC", "en-US"), "Oct 1, 10:05 PM");
  assert.equal(afkTime("2026-10-02T02:10:00Z", NOW, "America/Chicago", "en-US"), "Oct 1, 9:10 PM");
  assert.equal(afkTime("not a time", NOW, "UTC", "en-US"), "");
});

test("a click or key is the Overlord coming back only after the board saw none for a while, and only while AFK mode is on", () => {
  const on = Date.parse("2026-10-02T02:10:00Z");
  const cases: [string, Afk, number, number, Occasion][] = [
    ["his first click hours after it turned on", afk(), 0, NOW, "back"],
    ["a click right after he turned it on", afk(), 0, on + 20_000, ""],
    ["a click while he is still working on the board", afk(), on + 4 * 60_000, on + 6 * 60_000, ""],
    ["the first click after he was gone", afk(), on + 6 * 60_000, on + 6 * 60_000 + AWAY_MS, "back"],
    ["just short of the wait", afk(), on + 6 * 60_000, on + 6 * 60_000 + AWAY_MS - 1, ""],
    ["a click before it turned on does not count against the wait", afk(), on - 60 * 60_000, on + 60_000, ""],
    ["off", afk({ state: "off", since: "" }), 0, NOW, ""],
    ["a switch that cannot be read", afk({ state: "unreadable", since: "" }), 0, NOW, ""],
    ["on with a time the board cannot read", afk({ since: "not a time" }), 0, NOW, "back"],
  ];
  for (const [name, value, lastTouch, now, want] of cases) assert.equal(offerFor(value, lastTouch, now), want, name);
});

test("his first click or key after the CFO turned AFK mode on at his ask offers the switch back at once, and the next waits as usual", () => {
  const on = Date.parse("2026-10-02T02:10:00Z");
  const cases: [string, Afk, number, number, Occasion][] = [
    ["his first click a moment after the CFO's switch", afk(BY_THE_CFO), 0, on + 20_000, "asked"],
    ["his first since the switch, after one of his from before it", afk(BY_THE_CFO), on - 60_000, on + 20_000, "asked"],
    ["his next click, a moment after his first", afk(BY_THE_CFO), on + 20_000, on + 60_000, ""],
    ["his first click hours after the CFO's switch is his coming back", afk(BY_THE_CFO), 0, NOW, "back"],
    ["a switch he made himself waits as usual", afk(), 0, on + 20_000, ""],
    ["off", afk({ ...BY_THE_CFO, state: "off", since: "" }), 0, on + 20_000, ""],
  ];
  for (const [name, value, lastTouch, now, want] of cases) assert.equal(offerFor(value, lastTouch, now), want, name);
});

test("who made a switch is said to the Overlord: his own board or terminal without the program and its process, or the CFO at his ask with his words", () => {
  assert.equal(switchedBy("his own board (goblins-window.exe pid 4242)", ""), "from your board");
  assert.equal(switchedBy("his own terminal (powershell.exe pid 5151)", ""), "from your terminal");
  assert.equal(switchedBy("the board", ""), "from the board");
  assert.equal(switchedBy("", ""), "");
  assert.equal(switchedBy(BY_THE_CFO.from, ASKED), "by the CFO at your ask: “I'm stepping away, turn AFK on”");
});
