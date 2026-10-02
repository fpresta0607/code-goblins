import { test } from "node:test";
import assert from "node:assert/strict";
import { AFK_OFF, AWAY_MS, afkLine, afkTime, decisionSays, heldSays, heldWho, offersOff, parseAfkReport, safeLink, stillHeld, turnedOff, yours, type AfkDecision } from "./afk.ts";
import { parseSnapshot, type Afk, type AfkHeld } from "./types.ts";

const NOW = Date.parse("2026-10-02T12:31:00Z");
const held = (changes: Partial<AfkHeld> = {}): AfkHeld => ({ item: "question:drop-legacy-invoices", task: "", what: "Migration 0042 drops legacy_invoices. Apply it?", at: "2026-10-02T03:05:00Z", waiting: true, now: "still waiting on you", meanwhile: "", ...changes });
const afk = (changes: Partial<Afk> = {}): Afk => ({ state: "on", since: "2026-10-02T02:10:00Z", from: "his own board (goblins-window.exe pid 4242)", decided: 0, held: [], report: "", ended: "", problem: "", ...changes });
const snapshot = (value: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, instance: "fixture", ...value });

test("a supervisor from before AFK mode reached the board reads as off, and one that sends it is read whole", () => {
  assert.deepEqual(snapshot().afk, AFK_OFF);
  assert.deepEqual(snapshot({ afk: { state: "on", since: "2026-10-02T02:10:00Z", from: "his own terminal (powershell.exe pid 4242)", decided: 2, held: [{ item: "run:restart-db", what: "Restart the dev database", at: "2026-10-02T04:00:00Z", waiting: false, now: "succeeded" }] } }).afk,
    { state: "on", since: "2026-10-02T02:10:00Z", from: "his own terminal (powershell.exe pid 4242)", decided: 2, held: [{ item: "run:restart-db", task: "", what: "Restart the dev database", at: "2026-10-02T04:00:00Z", waiting: false, now: "succeeded", meanwhile: "" }], report: "", ended: "", problem: "" });
});

test("a state the board does not know stops the board rather than read as off", () => {
  assert.throws(() => snapshot({ afk: { state: "away" } }), /Invalid AFK state/);
  assert.throws(() => snapshot({ afk: { state: "on", held: [{ item: "question:q", waiting: "yes" }] } }), /Invalid response boolean/);
});

test("the bar says since when AFK mode is on and where it was turned on, how much the CFO decided and how much still waits on him", () => {
  const cases: [string, Afk, string][] = [
    ["off says nothing", afk({ state: "off", since: "" }), ""],
    ["on with nothing yet", afk(), "AFK since 2:10 AM, from your board. 0 decided, 0 held for you."],
    ["on from his terminal, counting only what still waits", afk({ from: "his own terminal (powershell.exe pid 5151)", decided: 4, held: [held(), held({ item: "run:restart-db", waiting: false, now: "succeeded" }), held({ item: "review:waiting-task-1-7", task: "task-1" })] }), "AFK since 2:10 AM, from your terminal. 4 decided, 2 held for you."],
    ["on with no word of where", afk({ from: "" }), "AFK since 2:10 AM. 0 decided, 0 held for you."],
    ["a switch that cannot be read is not taken for on", afk({ state: "unreadable", since: "", problem: "unexpected EOF" }), ""],
  ];
  for (const [name, value, want] of cases) assert.equal(afkLine(value, NOW, "UTC", "en-US"), want, name);
});

test("a time is the time of day, with its day when that is not today", () => {
  assert.equal(afkTime("2026-10-02T02:10:00Z", NOW, "UTC", "en-US"), "2:10 AM");
  assert.equal(afkTime("2026-10-01T22:05:00Z", NOW, "UTC", "en-US"), "Oct 1, 10:05 PM");
  assert.equal(afkTime("2026-10-02T02:10:00Z", NOW, "America/Chicago", "en-US"), "Oct 1, 9:10 PM");
  assert.equal(afkTime("not a time", NOW, "UTC", "en-US"), "");
});

test("a held item says whose it is, what became of it and what its goblin did meanwhile", () => {
  assert.equal(heldWho(held()), "CFO");
  assert.equal(heldWho(held({ task: "pd-billing-admin" })), "pd-billing-admin");
  assert.equal(heldSays(held()), "Still waiting on you.");
  assert.equal(heldSays(held({ waiting: false, now: "you answered it: Keep it held" })), "You answered it: Keep it held.");
  assert.equal(heldSays(held({ task: "pd-billing-admin", meanwhile: "working: moved on to the invoice export." })), "Still waiting on you. Meanwhile: working: moved on to the invoice export.");
  assert.equal(heldSays(held({ now: "" })), "");
  assert.deepEqual(stillHeld(afk({ held: [held(), held({ item: "run:x", waiting: false })] })).map((one) => one.item), ["question:drop-legacy-invoices"]);
});

test("a click or key is the Overlord coming back only after the board saw none for a while, and only while AFK mode is on", () => {
  const on = Date.parse("2026-10-02T02:10:00Z");
  const cases: [string, Afk, number, number, boolean][] = [
    ["his first click hours after it turned on", afk(), 0, NOW, true],
    ["a click right after he turned it on", afk(), 0, on + 20_000, false],
    ["a click while he is still working on the board", afk(), on + 4 * 60_000, on + 6 * 60_000, false],
    ["the first click after he was gone", afk(), on + 6 * 60_000, on + 6 * 60_000 + AWAY_MS, true],
    ["just short of the wait", afk(), on + 6 * 60_000, on + 6 * 60_000 + AWAY_MS - 1, false],
    ["a click before it turned on does not count against the wait", afk(), on - 60 * 60_000, on + 60_000, false],
    ["off", afk({ state: "off", since: "" }), 0, NOW, false],
    ["a switch that cannot be read", afk({ state: "unreadable", since: "" }), 0, NOW, false],
    ["on with a time the board cannot read", afk({ since: "not a time" }), 0, NOW, true],
  ];
  for (const [name, value, lastTouch, now, want] of cases) assert.equal(offersOff(value, lastTouch, now), want, name);
});

test("a decision says whose and what and what it stood on, names a pull request as a person would and an answer by its goblin, opens only a web link, and a merge word with no outcome says so", () => {
  const decision = (changes: Partial<AfkDecision> = {}): AfkDecision => ({ at: "2026-10-02T03:14:00Z", kind: "merge", what: "https://github.com/you/northwind-api/pull/412", link: "", evidence: "gate run 41 passed", outcome: "merged", task: "", ...changes });
  assert.deepEqual(decisionSays(decision()), { text: "northwind-api #412", href: "https://github.com/you/northwind-api/pull/412", outcome: "merged", basis: "Evidence: gate run 41 passed" });
  assert.equal(decisionSays(decision({ task: "northwind-invoices" })).text, "northwind-invoices: northwind-api #412");
  assert.deepEqual(decisionSays(decision({ outcome: "" })).outcome, "no outcome was recorded");
  assert.deepEqual(decisionSays(decision({ kind: "deploy", what: "northwind-api to production", link: "https://northwind.example/deploys/88", outcome: "" })), { text: "northwind-api to production", href: "https://northwind.example/deploys/88", outcome: "", basis: "Evidence: gate run 41 passed" });
  assert.deepEqual(decisionSays(decision({ kind: "answer", what: "notify-pd-billing-admin-41", task: "pd-billing-admin", evidence: "asked: Which CSV dialect? answered: RFC 4180", outcome: "" })),
    { text: "pd-billing-admin", href: "", outcome: "", basis: "Asked: Which CSV dialect? answered: RFC 4180" });
  assert.equal(decisionSays(decision({ kind: "answer", what: "notify-41", outcome: "" })).text, "notify-41", "an answer with no goblin named keeps its id");
  for (const unsafe of ["javascript:alert(1)", "http://plain.example/x", "file:///C:/secret", "https://two words.example", ""]) assert.equal(safeLink(unsafe), "", unsafe);
  assert.equal(decisionSays(decision({ kind: "other", what: "note", link: "javascript:alert(1)" })).href, "");
});

test("where the switch was turned is said to the Overlord as his own, without the program and its process", () => {
  assert.equal(yours("his own board (goblins-window.exe pid 4242)"), "your board");
  assert.equal(yours("his own terminal (powershell.exe pid 5151)"), "your terminal");
  assert.equal(yours("the board"), "the board");
  assert.equal(yours(""), "");
});

test("the report is shown when AFK mode turns off with a report kept, and at no other change", () => {
  const off = afk({ state: "off", since: "", report: "afk-20261002T021000.000Z", ended: "2026-10-02T12:31:00Z" });
  assert.equal(turnedOff(afk(), off), true);
  assert.equal(turnedOff(AFK_OFF, off), false, "the first snapshot a page sees");
  assert.equal(turnedOff(off, off), false);
  assert.equal(turnedOff(afk(), afk({ state: "off", since: "" })), false, "a reset keeps no report");
  assert.equal(turnedOff(afk({ state: "off" }), afk()), false);
});

test("the report is read with its sections in the supervisor's order, and none while no stretch has ended", () => {
  assert.equal(parseAfkReport({ found: false }), null);
  const report = parseAfkReport({
    found: true, session: "afk-20261002T021000.000Z", since: "2026-10-02T02:10:00Z", ended: "2026-10-02T12:31:00Z", lasted: "10h21m",
    from: "his own board (goblins-window.exe pid 4242)", ended_from: "his own terminal (powershell.exe pid 5151)",
    sections: [{ title: "Merged", entries: [{ at: "2026-10-02T03:14:00Z", kind: "merge", what: "https://github.com/you/northwind-api/pull/412", link: "https://github.com/you/northwind-api/pull/412", evidence: "gate run 41 passed", outcome: "merged" }] }, { title: "Deployed", entries: [] }],
    finished: [{ task: "northwind-invoices", pr: "https://github.com/you/northwind-api/pull/412", at: "2026-10-02T03:14:00Z" }],
    held: [held()], spent: ["claude week: 40% used when it turned on, 47% when it turned off (7 points)"], notes: [],
  });
  assert.deepEqual(report?.sections.map((section) => section.title + " " + section.entries.length), ["Merged 1", "Deployed 0"]);
  assert.deepEqual(report?.sections[0].entries[0], { at: "2026-10-02T03:14:00Z", kind: "merge", what: "https://github.com/you/northwind-api/pull/412", link: "https://github.com/you/northwind-api/pull/412", evidence: "gate run 41 passed", outcome: "merged", task: "" });
  assert.equal(report?.lasted, "10h21m");
  assert.deepEqual(report?.held, [held()]);
  assert.throws(() => parseAfkReport({ found: true, sections: "none" }), /Invalid response list/);
});
