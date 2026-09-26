import { test } from "node:test";
import assert from "node:assert/strict";
import { Dictation, dictationKey, dictationProblem, spoken, type Recognizer } from "./dictation.ts";

const key = (type: string, code: string, changes: Partial<{ key: string; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean; repeat: boolean }> = {}) =>
  ({ type, code, key: code === "Space" ? " " : code, ctrlKey: true, shiftKey: true, altKey: false, metaKey: false, repeat: false, ...changes });

test("holding Ctrl+Shift+Space starts dictation once and releasing any of its keys stops it", () => {
  assert.deepEqual(dictationKey(key("keydown", "Space")), { action: "start", swallow: true });
  assert.deepEqual(dictationKey(key("keydown", "Space", { repeat: true })), { action: null, swallow: true }, "a held key repeats without restarting");
  assert.deepEqual(dictationKey(key("keypress", "Space")), { action: null, swallow: true });
  assert.deepEqual(dictationKey(key("keyup", "Space")), { action: "stop", swallow: true });
  assert.deepEqual(dictationKey(key("keyup", "ShiftLeft", { key: "Shift", shiftKey: false })), { action: "stop", swallow: false }, "letting go of Shift first also stops, and Shift's release still reaches the terminal");
  assert.deepEqual(dictationKey(key("keyup", "ControlRight", { key: "Control", ctrlKey: false })), { action: "stop", swallow: false });
});

test("every other key belongs to the terminal", () => {
  for (const other of [key("keydown", "Space", { shiftKey: false }), key("keydown", "Space", { ctrlKey: false }), key("keydown", "Space", { altKey: true }), key("keydown", "Space", { metaKey: true }), key("keydown", "KeyA"), key("keyup", "KeyA")]) {
    assert.equal(dictationKey(other), null, JSON.stringify(other));
  }
});

test("what was heard is typed as one line of plain words", () => {
  assert.equal(spoken([" run the tests", " and then commit "]), "run the tests and then commit");
  assert.equal(spoken(["line one\nline two"]), "line one line two", "a line break would press Enter");
  assert.equal(spoken(["", "  "]), "");
  assert.equal(spoken([]), "");
});

test("a recognition failure is explained in plain words, and a deliberate stop says nothing", () => {
  assert.match(dictationProblem("not-allowed"), /microphone is blocked/);
  assert.match(dictationProblem("service-not-allowed"), /microphone is blocked/);
  assert.match(dictationProblem("audio-capture"), /No microphone/);
  assert.match(dictationProblem("network"), /network/);
  assert.match(dictationProblem("no-speech"), /Nothing was heard/);
  assert.equal(dictationProblem("aborted"), "");
  assert.match(dictationProblem("language-not-supported"), /language-not-supported/);
});

// A recognizer that plays back what the browser's would do.
class FakeRecognizer implements Recognizer {
  static made: FakeRecognizer[] = [];
  continuous = false;
  interimResults = true;
  lang = "";
  started = false;
  onresult: Recognizer["onresult"] = null;
  onerror: Recognizer["onerror"] = null;
  onend: Recognizer["onend"] = null;
  constructor() { FakeRecognizer.made.push(this); }
  start() { this.started = true; }
  stop() { this.onend?.(); }
  abort() { this.onerror?.({ error: "aborted" }); this.onend?.(); }
  hear(...finals: string[]) {
    const results = finals.map((transcript) => [{ transcript }]);
    this.onresult?.({ resultIndex: 0, results });
  }
}

function dictation(recognition: (new () => Recognizer) | null = FakeRecognizer) {
  FakeRecognizer.made = [];
  const heard: string[] = [], listening: boolean[] = [], problems: string[] = [];
  const subject = new Dictation({ heard: (text) => heard.push(text), listening: (on) => listening.push(on), problem: (note) => problems.push(note) }, () => recognition, "en-GB");
  return { subject, heard, listening, problems };
}

test("releasing the keys types every final phrase heard while they were held, once", () => {
  const { subject, heard, listening } = dictation();
  subject.start();
  subject.start();
  assert.equal(FakeRecognizer.made.length, 1, "a second start while listening opens no second microphone");
  const recognizer = FakeRecognizer.made[0];
  assert.equal(recognizer.started, true);
  assert.equal(recognizer.continuous, true);
  assert.equal(recognizer.interimResults, false);
  assert.equal(recognizer.lang, "en-GB");
  recognizer.hear("open the");
  recognizer.hear("pull request");
  assert.deepEqual(heard, [], "nothing is typed while the keys are held");
  subject.stop();
  assert.deepEqual(heard, ["open the pull request"]);
  assert.deepEqual(listening, [true, false]);
  subject.stop();
  assert.deepEqual(heard, ["open the pull request"]);
});

test("a browser without speech recognition says so and never listens", () => {
  const { subject, listening, problems } = dictation(null);
  subject.start();
  assert.deepEqual(listening, []);
  assert.match(problems.at(-1) || "", /no speech recognition/);
});

test("a blocked microphone is reported and types nothing", () => {
  const { subject, heard, problems } = dictation();
  subject.start();
  FakeRecognizer.made[0].onerror?.({ error: "not-allowed" });
  FakeRecognizer.made[0].onend?.();
  assert.deepEqual(heard, []);
  assert.match(problems.at(-1) || "", /microphone is blocked/);
});

test("closing the terminal while listening drops what was heard", () => {
  const { subject, heard, listening } = dictation();
  subject.start();
  FakeRecognizer.made[0].hear("half a thought");
  subject.dispose();
  assert.deepEqual(heard, []);
  assert.deepEqual(listening, [true]);
});
