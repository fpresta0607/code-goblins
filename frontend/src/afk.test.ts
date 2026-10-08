import { test } from "node:test";
import assert from "node:assert/strict";
import { AFK_OFF, AWAY_MS, afkLine, afkTime, decisionSays, heldRecommends, heldSays, heldWho, offerFor, parseAfkReport, reportAction, safeLink, stillWaiting, switchedBy, turnedOff, allowanceGraph, allowanceSays, shownUnder, type AfkDecision, type AfkAllowance, type Occasion, type ReportAction } from "./afk.ts";
import { parseSnapshot, type Afk, type AfkHeld } from "./types.ts";

const NOW = Date.parse("2026-10-02T12:31:00Z");
const held = (changes: Partial<AfkHeld> = {}): AfkHeld => ({ item: "question:drop-legacy-invoices", task: "", what: "Migration 0042 drops legacy_invoices. Apply it?", at: "2026-10-02T03:05:00Z", waiting: true, now: "still waiting on you", meanwhile: "", recommendation: "", ...changes });
const afk = (changes: Partial<Afk> = {}): Afk => ({ state: "on", since: "2026-10-02T02:10:00Z", from: "his own board (goblins-window.exe pid 4242)", asked: "", decided: 0, held: [], report: "", ...changes });
// AFK mode as the CFO turned it on at his ask.
const ASKED = "I'm stepping away, turn AFK on";
const BY_THE_CFO = { from: "the CFO at his ask (claude pid 4242)", asked: ASKED };
const snapshot = (value: Record<string, unknown> = {}) => parseSnapshot({ healthy: true, instance: "fixture", ...value });

test("a supervisor from before AFK mode reached the board reads as off, and one that sends it is read whole", () => {
  assert.deepEqual(snapshot().afk, AFK_OFF);
  assert.deepEqual(snapshot({ afk: { state: "on", since: "2026-10-02T02:10:00Z", from: "his own terminal (powershell.exe pid 4242)", decided: 2, held: [{ item: "run:restart-db", what: "Restart the dev database", at: "2026-10-02T04:00:00Z", waiting: false, now: "succeeded" }, { item: "question:drop-legacy-invoices", what: "Apply it?", at: "2026-10-02T04:01:00Z", waiting: true, now: "still waiting on you", recommendation: "Keep it held" }] } }).afk,
    { state: "on", since: "2026-10-02T02:10:00Z", from: "his own terminal (powershell.exe pid 4242)", asked: "", decided: 2, held: [
      { item: "run:restart-db", task: "", what: "Restart the dev database", at: "2026-10-02T04:00:00Z", waiting: false, now: "succeeded", meanwhile: "", recommendation: "" },
      { item: "question:drop-legacy-invoices", task: "", what: "Apply it?", at: "2026-10-02T04:01:00Z", waiting: true, now: "still waiting on you", meanwhile: "", recommendation: "Keep it held" },
    ], report: "" });
  assert.equal(snapshot({ afk: { state: "on", since: "2026-10-02T02:10:00Z", ...BY_THE_CFO } }).afk.asked, ASKED);
});

test("a state the board does not know stops the board rather than read as off", () => {
  assert.throws(() => snapshot({ afk: { state: "away" } }), /Invalid AFK state/);
  assert.throws(() => snapshot({ afk: { state: "on", held: [{ item: "question:q", waiting: "yes" }] } }), /Invalid response boolean/);
});

test("the bar says since when AFK mode is on, who turned it on and how much the CFO decided, and nothing of what is held, since nothing is held for him", () => {
  const cases: [string, Afk, string][] = [
    ["off says nothing", afk({ state: "off", since: "" }), ""],
    ["on with nothing yet", afk(), "AFK since 2:10 AM, from your board. 0 decided."],
    ["on by the CFO at his ask, without his words, which the offer and the report quote", afk({ ...BY_THE_CFO, decided: 1 }), "AFK since 2:10 AM, turned on by the CFO at your ask. 1 decided."],
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

test("a held item says whose it is, what became of it and what its goblin did meanwhile", () => {
  assert.equal(heldWho(held()), "CFO");
  assert.equal(heldWho(held({ task: "pd-billing-admin" })), "pd-billing-admin");
  assert.equal(heldSays(held()), "Still waiting on you.");
  assert.equal(heldSays(held({ waiting: false, now: "you answered it: Keep it held" })), "You answered it: Keep it held.");
  assert.equal(heldSays(held({ task: "pd-billing-admin", meanwhile: "working: moved on to the invoice export." })), "Still waiting on you. Meanwhile: working: moved on to the invoice export.");
  assert.equal(heldSays(held({ now: "" })), "");
  assert.deepEqual(stillWaiting([held(), held({ item: "run:x", waiting: false })]).map((one) => one.item), ["question:drop-legacy-invoices"]);
});

test("a held item says what was recommended for it and by whom: in the present while it waits on him, in the past once it does not, and nothing when nothing was", () => {
  assert.equal(heldRecommends(held({ recommendation: "Keep it held" })), "The CFO recommends: Keep it held.");
  assert.equal(heldRecommends(held({ task: "pd-billing-admin", recommendation: "Hold it.", waiting: false, now: "you answered it: Do it" })), "pd-billing-admin recommended: Hold it.");
  assert.equal(heldRecommends(held()), "");
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

test("a decision says whose and what and what it stood on, names a pull request as a person would and an answer by its goblin, opens only a web link, and a merge word with no outcome says so", () => {
  const decision = (changes: Partial<AfkDecision> = {}): AfkDecision => ({ at: "2026-10-02T03:14:00Z", kind: "merge", what: "https://github.com/you/northwind-api/pull/412", link: "", evidence: "gate run 41 passed", outcome: "merged", task: "", ...changes });
  assert.deepEqual(decisionSays(decision()), { text: "northwind-api #412", href: "https://github.com/you/northwind-api/pull/412", outcome: "merged", basis: "Evidence: gate run 41 passed" });
  assert.equal(decisionSays(decision({ task: "northwind-invoices" })).text, "northwind-invoices: northwind-api #412");
  assert.deepEqual(decisionSays(decision({ outcome: "" })).outcome, "no outcome was recorded");
  assert.deepEqual(decisionSays(decision({ kind: "deploy", what: "northwind-api to production", link: "https://northwind.example/deploys/88", outcome: "" })), { text: "northwind-api to production", href: "https://northwind.example/deploys/88", outcome: "", basis: "Evidence: gate run 41 passed" });
  assert.deepEqual(decisionSays(decision({ kind: "answer", what: "notify-pd-billing-admin-41", task: "pd-billing-admin", evidence: "asked: Which CSV dialect? answered: RFC 4180", outcome: "" })),
    { text: "pd-billing-admin", href: "", outcome: "", basis: "Asked: Which CSV dialect? Answered: RFC 4180" });
  assert.equal(decisionSays(decision({ kind: "answer", what: "notify-41", outcome: "" })).text, "notify-41", "an answer with no goblin named keeps its id");
  for (const unsafe of ["javascript:alert(1)", "http://plain.example/x", "file:///C:/secret", "https://two words.example", ""]) assert.equal(safeLink(unsafe), "", unsafe);
  assert.equal(decisionSays(decision({ kind: "other", what: "note", link: "javascript:alert(1)" })).href, "");
});

test("who made a switch is said to the Overlord: his own board or terminal without the program and its process, or the CFO at his ask with his words", () => {
  assert.equal(switchedBy("his own board (goblins-window.exe pid 4242)", ""), "from your board");
  assert.equal(switchedBy("his own terminal (powershell.exe pid 5151)", ""), "from your terminal");
  assert.equal(switchedBy("the board", ""), "from the board");
  assert.equal(switchedBy("", ""), "");
  assert.equal(switchedBy(BY_THE_CFO.from, ASKED), "by the CFO at your ask: “I'm stepping away, turn AFK on”");
});

test("the report is shown when AFK mode turns off with a report kept, and at no other change", () => {
  const off = afk({ state: "off", since: "", report: "afk-20261002T021000.000Z" });
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
    held: [held()], spent: [{ provider: "claude", window: "week", on: 40, off: 47 }], notes: [],
  });
  assert.deepEqual(report?.sections.map((section) => section.title + " " + section.entries.length), ["Merged 1", "Deployed 0"]);
  assert.deepEqual(report?.sections[0].entries[0], { at: "2026-10-02T03:14:00Z", kind: "merge", what: "https://github.com/you/northwind-api/pull/412", link: "https://github.com/you/northwind-api/pull/412", evidence: "gate run 41 passed", outcome: "merged", task: "" });
  assert.equal(report?.lasted, "10h21m");
  assert.deepEqual([report?.asked, report?.ended_asked], ["", ""], "he made both switches himself");
  assert.deepEqual(report?.held, [held()]);
  const asked = parseAfkReport({ found: true, ...BY_THE_CFO, ended_from: "the CFO at his ask (claude pid 4242)", ended_asked: "I'm back, turn AFK off", sections: [], finished: [], held: [], spent: [], notes: [] });
  assert.deepEqual([asked?.asked, asked?.ended_asked], [ASKED, "I'm back, turn AFK off"]);
  assert.throws(() => parseAfkReport({ found: true, sections: "none" }), /Invalid response list/);
});

test("the report's Spent reads each allowance as the percent used at either end, a reading not taken as none, and credits as what was spent", () => {
  const report = parseAfkReport({ found: true, sections: [], finished: [], held: [], notes: [], spent: [
    { provider: "claude", window: "week", on: 29, off: 35 },
    { provider: "claude", window: "session", on: 42, off: 3, reset: true },
    { provider: "codex", window: "week", off: 4 },
    { provider: "codex", window: "credits", credits: true, spent: 12.5, unit: "credits" },
  ] });
  assert.deepEqual(report?.spent, [
    { provider: "claude", window: "week", on: 29, off: 35, reset: false, credits: false, spent: 0, unit: "" },
    { provider: "claude", window: "session", on: 42, off: 3, reset: true, credits: false, spent: 0, unit: "" },
    { provider: "codex", window: "week", on: null, off: 4, reset: false, credits: false, spent: 0, unit: "" },
    { provider: "codex", window: "credits", on: null, off: null, reset: false, credits: true, spent: 12.5, unit: "credits" },
  ]);
  assert.throws(() => parseAfkReport({ found: true, spent: [{ provider: "claude", window: "week", on: "29%" }] }), /Invalid response number/);
});

const allowance = (changes: Partial<AfkAllowance>): AfkAllowance => ({ provider: "claude", window: "week", on: null, off: null, reset: false, credits: false, spent: 0, unit: "", ...changes });

test("Spent shows each weekly limit and the credits spent, and no shorter window such as Claude's five hours", () => {
  const cases: [string, AfkAllowance, boolean][] = [
    ["Claude's week", allowance({ on: 29, off: 35 }), true],
    ["a model's week", allowance({ window: "Fable week", on: 12.5 }), true],
    ["Codex's week", allowance({ provider: "codex", off: 4 }), true],
    ["Claude's five-hour session", allowance({ window: "session", on: 42, off: 3, reset: true }), false],
    ["credits", allowance({ provider: "codex", window: "credits", credits: true, spent: 12.5, unit: "credits" }), true],
  ];
  for (const [name, value, want] of cases) assert.equal(shownUnder(value), want, name);
});

test("a weekly limit under Spent says how much of it is left when AFK turned off and how much AFK used, and credits what was spent", () => {
  const cases: [string, AfkAllowance, { name: string; value: string; change: string; label: string }][] = [
    ["read at both ends", allowance({ on: 29, off: 35 }), { name: "Claude weekly limit", value: "65% left", change: "AFK used 6%", label: "Claude weekly limit: 65% left, from 29% to 35% used while AFK was on" }],
    ["one percent used", allowance({ provider: "codex", on: 1, off: 2 }), { name: "Codex weekly limit", value: "98% left", change: "AFK used 1%", label: "Codex weekly limit: 98% left, from 1% to 2% used while AFK was on" }],
    ["none used", allowance({ on: 52, off: 52 }), { name: "Claude weekly limit", value: "48% left", change: "AFK used 0%", label: "Claude weekly limit: 48% left, from 52% to 52% used while AFK was on" }],
    ["a limit that renewed", allowance({ on: 92, off: 3, reset: true }), { name: "Claude weekly limit", value: "97% left", change: "renewed", label: "Claude weekly limit: 97% left, renewed while AFK was on" }],
    ["read only at turn-on", allowance({ window: "Fable week", on: 12.5 }), { name: "Claude Fable weekly limit", value: "87.5% left", change: "", label: "Claude Fable weekly limit: 87.5% left at AFK on" }],
    ["read only at turn-off", allowance({ provider: "codex", off: 4 }), { name: "Codex weekly limit", value: "96% left", change: "", label: "Codex weekly limit: 96% left at AFK off" }],
    ["credits", allowance({ provider: "codex", window: "credits", credits: true, spent: 12.5, unit: "credits" }), { name: "Codex credits", value: "12.5 credits spent", change: "", label: "Codex credits: 12.5 credits spent" }],
  ];
  for (const [name, value, want] of cases) assert.deepEqual(allowanceSays(value), want, name);
});

test("an allowance's graph marks what was used before AFK and the stretch AFK used, from nothing after a reset, and there is no graph for a limit read at one end or for credits", () => {
  const cases: [string, AfkAllowance, { before: number; from: number; to: number } | null][] = [
    ["read at both ends", allowance({ on: 29, off: 35 }), { before: 29, from: 29, to: 35 }],
    ["a window that reset", allowance({ on: 42, off: 3, reset: true }), { before: 0, from: 0, to: 3 }],
    // 52% read only when AFK turned off may all have been used while it was
    // on, so none of it is drawn as used before.
    ["read only at turn-off", allowance({ off: 52 }), null],
    ["read only at turn-on", allowance({ on: 12.5 }), null],
    ["credits", allowance({ credits: true, spent: 12.5 }), null],
  ];
  for (const [name, value, want] of cases) assert.deepEqual(allowanceGraph(value), want, name);
});

test("the report's main button is the Command Center while anything it held still waits on him, and the board otherwise", () => {
  const cases: [string, AfkHeld[], ReportAction][] = [
    ["nothing held", [], "board"],
    ["held and answered since", [held({ waiting: false, now: "you answered it: Keep it held" })], "board"],
    ["one of two still waits", [held({ item: "run:restart-db", waiting: false, now: "succeeded" }), held()], "command"],
  ];
  for (const [name, value, want] of cases) assert.equal(reportAction(value), want, name);
});
