import test from "node:test";
import assert from "node:assert/strict";
import { BOARD_DICTATIONS, BOARD_PANES, hostPane, readDictations, rememberDictation, voiceLevel } from "./voice.ts";

class MemoryStorage {
  values = new Map<string, string>();
  getItem(key: string) { return this.values.get(key) ?? null; }
  setItem(key: string, value: string) { this.values.set(key, value); }
}

test("the board's own dictations stay in this browser, newest first, a bounded few per pane", () => {
  const storage = new MemoryStorage();
  for (let i = 1; i <= BOARD_DICTATIONS + 2; i++) rememberDictation(storage, "cfo", "message " + i, i * 1000);
  rememberDictation(storage, "task:build", "for the goblin", 500);
  const cfo = readDictations(storage, "cfo");
  assert.equal(cfo.length, BOARD_DICTATIONS);
  assert.deepEqual(cfo[0], { text: "message " + (BOARD_DICTATIONS + 2), at: (BOARD_DICTATIONS + 2) * 1000 });
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
  assert.deepEqual(readDictations(storage, "cfo"), [{ text: "kept", at: 5 }], "a broken entry or pane leaves the rest");
  assert.deepEqual(readDictations(storage, "other"), []);
  const refusing = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("blocked"); } };
  assert.deepEqual(readDictations(refusing, "cfo"), []);
  assert.deepEqual(rememberDictation(refusing, "cfo", "still shown", 1).map((message) => message.text), ["still shown"]);
  assert.deepEqual(readDictations(null, "cfo"), []);
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
