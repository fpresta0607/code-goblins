import { test } from "node:test";
import assert from "node:assert/strict";
import { Dictation, dictationKey, dictationProblem, spoken, type Capture, type Recognizer } from "./dictation.ts";

const key = (type: string, code: string, changes: Partial<{ key: string; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean; repeat: boolean }> = {}) =>
  ({ type, code, key: code === "Space" ? " " : code, ctrlKey: true, shiftKey: true, altKey: false, metaKey: false, repeat: false, ...changes });

test("holding Ctrl+Shift+Space starts dictation once and releasing any of its keys stops it", () => {
  assert.deepEqual(dictationKey(key("keydown", "Space")), { action: "start", swallow: true });
  assert.deepEqual(dictationKey(key("keydown", "Space", { repeat: true })), { action: null, swallow: true }, "a held key repeats without restarting");
  assert.deepEqual(dictationKey(key("keypress", "Space")), { action: null, swallow: true });
  assert.deepEqual(dictationKey(key("keyup", "Space")), { action: "stop", swallow: true });
  assert.deepEqual(dictationKey(key("keyup", "ShiftLeft", { key: "Shift", shiftKey: false })), { action: "stop", swallow: false }, "letting go of Shift first also stops, and Shift's release still reaches the terminal");
  assert.deepEqual(dictationKey(key("keyup", "ControlRight", { key: "Control", ctrlKey: false })), { action: "stop", swallow: false });
  assert.deepEqual(dictationKey(key("keyup", "Space", { ctrlKey: false, shiftKey: false })), { action: "stop", swallow: false }, "Space released after Ctrl and Shift still stops");
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

// A microphone that opens when told to, handing over one track and its level.
function microphone() {
  const opened: { track: MediaStreamTrack; closed: boolean; level: number }[] = [];
  let answer: { resolve: () => void; reject: (error: Error) => void } | null = null;
  const open = () => new Promise<Capture>((resolve, reject) => {
    answer = {
      resolve: () => {
        const capture = { track: { kind: "audio" } as MediaStreamTrack, closed: false, level: .6 };
        opened.push(capture);
        resolve({ track: capture.track, level: () => capture.closed ? 0 : capture.level, close: () => { capture.closed = true; } });
      },
      reject,
    };
  });
  return { open, opened, allow: () => answer?.resolve(), refuse: (name: string) => answer?.reject(Object.assign(new Error(name), { name })) };
}
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

class TrackRecognizer extends FakeRecognizer {
  heardFrom: unknown = "nothing";
  start(track?: unknown) { this.heardFrom = track; this.started = true; }
}

function listened(recognition: new () => Recognizer = TrackRecognizer) {
  FakeRecognizer.made = [];
  const mic = microphone();
  const heard: string[] = [], listening: boolean[] = [], problems: string[] = [];
  const subject = new Dictation({ heard: (text) => heard.push(text), listening: (on) => listening.push(on), problem: (note) => problems.push(note) }, () => recognition, "en-GB", mic.open);
  return { subject, mic, heard, listening, problems };
}

test("the recognizer hears the one microphone track whose level the waveform shows", async () => {
  const { subject, mic, heard, listening } = listened();
  subject.start();
  assert.deepEqual(listening, [true], "the bubble shows listening at once");
  assert.equal(subject.level(), 0, "no level before the microphone opens");
  mic.allow();
  await settle();
  const recognizer = FakeRecognizer.made[0] as TrackRecognizer;
  assert.equal(mic.opened.length, 1, "one capture, never a second");
  assert.equal(recognizer.heardFrom, mic.opened[0].track, "the recognizer listens to that same track");
  assert.equal(subject.level(), .6);
  recognizer.hear("ship it");
  subject.stop();
  assert.deepEqual(heard, ["ship it"]);
  assert.equal(mic.opened[0].closed, true, "stopping closes the microphone");
  assert.equal(subject.level(), 0);
});

test("releasing the keys before the microphone opens starts nothing and keeps nothing open", async () => {
  const { subject, mic, heard, listening } = listened();
  subject.start();
  subject.stop();
  assert.deepEqual(listening, [true, false]);
  mic.allow();
  await settle();
  assert.equal((FakeRecognizer.made[0] as TrackRecognizer).started, false);
  assert.equal(mic.opened[0].closed, true, "a microphone that opens too late is closed at once");
  assert.deepEqual(heard, []);
  subject.start();
  assert.equal(FakeRecognizer.made.length, 2, "the next press starts afresh");
});

test("a microphone the browser refuses is explained and nothing listens", async () => {
  const { subject, mic, listening, problems } = listened();
  subject.start();
  mic.refuse("NotAllowedError");
  await settle();
  assert.deepEqual(listening, [true, false]);
  assert.match(problems.at(-1) || "", /microphone is blocked/);
  const missing = listened();
  missing.subject.start();
  missing.mic.refuse("NotFoundError");
  await settle();
  assert.match(missing.problems.at(-1) || "", /No microphone/);
});

test("a recognizer that cannot take a track still dictates, without a level or a held microphone", async () => {
  class TracklessRecognizer extends FakeRecognizer {
    start(track?: unknown) { if (track !== undefined) throw new TypeError("parameter 1 is not of type 'MediaStreamTrack'"); this.started = true; }
  }
  const { subject, mic, heard } = listened(TracklessRecognizer);
  subject.start();
  mic.allow();
  await settle();
  assert.equal(FakeRecognizer.made[0].started, true);
  assert.equal(mic.opened[0].closed, true);
  assert.equal(subject.level(), 0);
  FakeRecognizer.made[0].hear("still typed");
  subject.stop();
  assert.deepEqual(heard, ["still typed"]);
});

test("closing the terminal while the microphone opens leaves nothing open", async () => {
  const { subject, mic, listening } = listened();
  subject.start();
  subject.dispose();
  mic.allow();
  await settle();
  assert.equal(mic.opened[0].closed, true);
  assert.deepEqual(listening, [true]);
});
