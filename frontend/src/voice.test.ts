import test from "node:test";
import assert from "node:assert/strict";
import { parseDictations, voiceLevel } from "./voice.ts";

test("the supervisor's list reads as it keeps it, newest first", () => {
  // Arrange
  const kept = { messages: [{ text: "then run the tests", at: 2000 }, { text: "open the pull request", at: 1000 }] };

  // Act
  const read = parseDictations(kept);

  // Assert
  assert.deepEqual(read, kept.messages);
});

for (const [name, value, expected] of [
  ["a list with no entries", { messages: [] }, []],
  ["a list with none kept yet", {}, []],
  ["a broken entry among good ones", { messages: [{ text: 3, at: "x" }, { text: "kept", at: 5 }, { text: "", at: 6 }, { text: "no time" }, "no"] }, [{ text: "kept", at: 5 }]],
] as const) {
  test(`${name} reads as only its good entries`, () => {
    assert.deepEqual(parseDictations(value), expected);
  });
}

test("the voice level is silence at rest, rises with loudness and never passes one", () => {
  const frame = (amplitude: number) => Array.from({ length: 256 }, (_, i) => 128 + Math.round(amplitude * Math.sin(i / 4)));
  assert.equal(voiceLevel(frame(0)), 0);
  const quiet = voiceLevel(frame(6)), speech = voiceLevel(frame(30)), shout = voiceLevel(frame(127));
  assert.ok(quiet > 0 && quiet < speech && speech < shout, JSON.stringify({ quiet, speech, shout }));
  assert.ok(speech > .3, "ordinary speech fills a good part of the bars");
  assert.equal(shout, 1);
  assert.equal(voiceLevel([]), 0);
});
