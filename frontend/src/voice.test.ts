import test from "node:test";
import assert from "node:assert/strict";
import { BOARD_DICTATIONS, BOARD_PANES, hostPane, parseSiqspeak, readDictations, recentMessages, rememberDictation, voiceLevel, type VoiceMessage } from "./voice.ts";

class MemoryStorage {
  values = new Map<string, string>();
  getItem(key: string) { return this.values.get(key) ?? null; }
  setItem(key: string, value: string) { this.values.set(key, value); }
}

test("SIQspeak's report reads as its state and its transcriptions, newest first", () => {
  const report = parseSiqspeak({ state: "running", entries: [
    { text: "Show me what needs my attention.", timestamp: "2026-09-29T12:45:00", time_epoch: 1790000700.5 },
    { text: "Open the board task terminal.", timestamp: "2026-09-29T12:42:00", time_epoch: 1790000520 },
  ], has_skipped: false });
  assert.equal(report.state, "running");
  assert.deepEqual(report.messages, [
    { text: "Show me what needs my attention.", at: 1790000700500, source: "siqspeak" },
    { text: "Open the board task terminal.", at: 1790000520000, source: "siqspeak" },
  ]);
});

test("a SIQspeak report the board cannot trust reads as unreadable with no words", () => {
  for (const value of [{ state: "sleeping", entries: [] }, { state: "running", entries: "no" }, null, "running"]) {
    assert.deepEqual(parseSiqspeak(value), { state: "unreadable", messages: [] }, JSON.stringify(value));
  }
  assert.deepEqual(parseSiqspeak({ state: "stopped" }), { state: "stopped", messages: [] });
  assert.deepEqual(parseSiqspeak({ state: "missing", entries: [] }), { state: "missing", messages: [] });
  assert.deepEqual(parseSiqspeak({ state: "running", entries: [{ text: "", time_epoch: 1 }, { text: "kept", time_epoch: "late" }] }).messages, [{ text: "kept", at: 0, source: "siqspeak" }]);
});

test("the board's own dictations stay in this browser, newest first, a bounded few per pane", () => {
  const storage = new MemoryStorage();
  for (let i = 1; i <= BOARD_DICTATIONS + 2; i++) rememberDictation(storage, "cfo", "message " + i, i * 1000);
  rememberDictation(storage, "task:build", "for the goblin", 500);
  const cfo = readDictations(storage, "cfo");
  assert.equal(cfo.length, BOARD_DICTATIONS);
  assert.deepEqual(cfo[0], { text: "message " + (BOARD_DICTATIONS + 2), at: (BOARD_DICTATIONS + 2) * 1000, source: "board" });
  assert.equal(cfo.at(-1)?.text, "message 3");
  assert.deepEqual(readDictations(storage, "task:build").map((message) => message.text), ["for the goblin"]);
});

test("only the panes used most recently keep their dictations", () => {
  const storage = new MemoryStorage();
  for (let i = 0; i <= BOARD_PANES; i++) rememberDictation(storage, "task:" + i, "said in pane " + i, 1000 + i);
  assert.deepEqual(readDictations(storage, "task:0"), [], "the pane used longest ago is let go");
  assert.equal(readDictations(storage, "task:1").length, 1);
  assert.equal(readDictations(storage, "task:" + BOARD_PANES)[0].text, "said in pane " + BOARD_PANES);
  assert.equal(Object.keys(JSON.parse(storage.getItem("cfo-dictations-v1") || "{}")).length, BOARD_PANES);
});

test("a broken or unavailable store reads as no dictations and never throws", () => {
  const storage = new MemoryStorage();
  storage.setItem("cfo-dictations-v1", "{not json");
  assert.deepEqual(readDictations(storage, "cfo"), []);
  storage.setItem("cfo-dictations-v1", JSON.stringify({ cfo: [{ text: 3, at: "x" }, { text: "kept", at: 5 }], other: "no" }));
  assert.deepEqual(readDictations(storage, "cfo"), [{ text: "kept", at: 5, source: "board" }], "a broken entry or pane leaves the rest");
  assert.deepEqual(readDictations(storage, "other"), []);
  const refusing = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("blocked"); } };
  assert.deepEqual(readDictations(refusing, "cfo"), []);
  assert.deepEqual(rememberDictation(refusing, "cfo", "still shown", 1).map((message) => message.text), ["still shown"]);
  assert.deepEqual(readDictations(null, "cfo"), []);
});

test("recent messages mix SIQspeak's and the board's, newest first, five at most", () => {
  const siqspeak: VoiceMessage[] = [{ text: "b", at: 40, source: "siqspeak" }, { text: "d", at: 10, source: "siqspeak" }];
  const board: VoiceMessage[] = [{ text: "a", at: 50, source: "board" }, { text: "c", at: 20, source: "board" }];
  assert.deepEqual(recentMessages(siqspeak, board).map((message) => message.text), ["a", "b", "c", "d"]);
  const more: VoiceMessage[] = [...board, { text: "e", at: 5, source: "board" }, { text: "f", at: 1, source: "board" }];
  assert.deepEqual(recentMessages(siqspeak, more).map((message) => message.text), ["a", "b", "c", "d", "e"]);
  assert.deepEqual(recentMessages([], []), []);
});

test("a native host's pane is its goblin's task whatever its generation, or the CFO", () => {
  for (const [query, pane] of [
    ["task=t-1&generation=g-1", "t-1"],
    ["task=t-1&generation=g-2", "t-1"],
    ["cfo=cfo%3A1", "cfo"],
    ["", "cfo"],
  ]) assert.equal(hostPane(query), pane, query);
});

test("the voice level is silence at rest, rises with loudness and never passes one", () => {
  const frame = (amplitude: number) => Array.from({ length: 256 }, (_, i) => 128 + Math.round(amplitude * Math.sin(i / 4)));
  assert.equal(voiceLevel(frame(0)), 0);
  const quiet = voiceLevel(frame(6)), speech = voiceLevel(frame(30)), shout = voiceLevel(frame(127));
  assert.ok(quiet > 0 && quiet < speech && speech < shout, JSON.stringify({ quiet, speech, shout }));
  assert.ok(speech > .3, "ordinary speech fills a good part of the bars");
  assert.equal(shout, 1);
  assert.equal(voiceLevel([]), 0);
});
