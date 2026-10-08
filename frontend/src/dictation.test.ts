import { test } from "node:test";
import assert from "node:assert/strict";
import { Dictation, dictationKey, dictationProblem, inDesktopApp, microphoneProblem, spoken, type Capture, type Recognizer } from "./dictation.ts";

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

test("a refused microphone says where to allow it: Windows in the desktop app, the browser in a tab", () => {
  for (const name of ["NotAllowedError", "SecurityError"]) {
    assert.match(microphoneProblem(name, true), /^The microphone is blocked for Code Goblins by Windows\. .*Windows Settings > Privacy & security > Microphone/);
    assert.doesNotMatch(microphoneProblem(name, true), /browser/);
    assert.equal(microphoneProblem(name, false), dictationProblem("not-allowed"));
  }
  for (const app of [true, false]) {
    assert.match(microphoneProblem("NotFoundError", app), /No microphone/);
    assert.match(microphoneProblem("AbortError", app), /could not be opened/);
  }
  assert.equal(inDesktopApp(), false, "the unit tests run with no window");
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

function dictation(recognition: new () => Recognizer = FakeRecognizer) {
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
  assert.deepEqual(listening, [], "the bubble must not invite speech before capture is ready");
  assert.equal(subject.level(), 0, "no level before the microphone opens");
  mic.allow();
  await settle();
  const recognizer = FakeRecognizer.made[0] as TrackRecognizer;
  assert.equal(recognizer.started, true);
  assert.deepEqual(listening, [true], "listening begins once the recognizer takes the track");
  assert.equal(mic.opened.length, 1, "one capture, never a second");
  assert.equal(recognizer.heardFrom, mic.opened[0].track, "the recognizer listens to that same track");
  assert.equal(subject.level(), .6);
  recognizer.hear("ship it");
  subject.stop();
  assert.deepEqual(heard, ["ship it"]);
  assert.equal(mic.opened[0].closed, true, "stopping closes the microphone");
  assert.equal(subject.level(), 0);
});

// A recognizer that says its words when told to, as the board's own does
// while the supervisor recognises the sound.
class SlowRecognizer extends TrackRecognizer {
  stopped = false;
  abortCount = 0;
  stop() { this.stopped = true; }
  abort() { this.abortCount++; super.abort(); }
  answer(...finals: string[]) { if (finals.length) this.hear(...finals); this.onend?.(); }
}

test("at release the microphone closes and the bubble goes idle, and the words are typed when the recognizer has them", async () => {
  const { subject, mic, heard, listening } = listened(SlowRecognizer);
  subject.start();
  mic.allow();
  await settle();
  subject.stop();
  const recognizer = FakeRecognizer.made[0] as SlowRecognizer;
  assert.equal(recognizer.stopped, true);
  assert.deepEqual(listening, [true, false], "the bubble is idle as soon as the keys are released");
  assert.equal(mic.opened[0].closed, true, "the microphone closes without waiting for the words");
  assert.equal(subject.level(), 0);
  assert.deepEqual(heard, []);
  recognizer.answer("ship it");
  assert.deepEqual(heard, ["ship it"]);
  assert.deepEqual(listening, [true, false], "the words arriving change nothing the release already did");
});

test("a second dictation starts while the first is still being recognised, and both are typed", async () => {
  const { subject, mic, heard, listening } = listened(SlowRecognizer);
  subject.start();
  mic.allow();
  await settle();
  subject.stop();
  subject.start();
  assert.equal(FakeRecognizer.made.length, 2, "the next press is not lost to the words still on their way");
  mic.allow();
  await settle();
  assert.equal(mic.opened.length, 2);
  assert.equal(mic.opened[1].closed, false);
  subject.stop();
  const [first, second] = FakeRecognizer.made as SlowRecognizer[];
  first.answer("open the pull request");
  second.answer("and merge it");
  assert.deepEqual(heard, ["open the pull request", "and merge it"]);
  assert.deepEqual(listening, [true, false, true, false]);
  assert.equal(mic.opened[1].closed, true);
});

test("overlapping dictations type in capture order when replies finish in reverse", async () => {
  for (const count of [2, 3]) {
    const { subject, mic, heard, listening } = listened(SlowRecognizer);
    const phrases = ["open the\npull request", "then run the tests", "then commit"].slice(0, count);
    try {
      for (let index = 0; index < count; index++) {
        subject.start();
        mic.allow();
        await settle();
        subject.stop();
        assert.equal(mic.opened[index].closed, true, "release closes each microphone before any reply");
      }
      assert.equal(FakeRecognizer.made.length, count, "the next capture starts while earlier words are pending");
      assert.deepEqual(listening, Array.from({ length: count }, () => [true, false]).flat());
      const recognizers = FakeRecognizer.made as SlowRecognizer[];
      for (let index = count - 1; index > 0; index--) {
        recognizers[index].answer(phrases[index]);
        assert.deepEqual(heard, [], "a later reply waits for the earlier capture");
      }
      recognizers[0].answer(phrases[0]);
      const expected = phrases.map((phrase) => phrase.replace(/\s+/g, " "));
      assert.deepEqual(heard, expected);
      for (const recognizer of recognizers) recognizer.answer("duplicate\nreply");
      assert.deepEqual(heard, expected, "late callbacks insert nothing twice");
      assert.equal(heard.some((text) => /[\r\n]/.test(text)), false, "dictation never presses Enter");
    } finally {
      subject.dispose();
    }
  }
});

test("an empty or refused earlier dictation releases later words without typing a gap", () => {
  const refusal = "Dictation needs 1 GB of free memory and 1 GB of free commit.";
  for (const gap of ["empty", "refused", "canceled", "silence"]) {
    const { subject, heard, problems } = dictation(SlowRecognizer);
    try {
      subject.start();
      subject.stop();
      subject.start();
      subject.stop();
      const [first, second] = FakeRecognizer.made;
      assert.ok(first instanceof SlowRecognizer);
      assert.ok(second instanceof SlowRecognizer);
      second.answer("and\nmerge it");
      assert.deepEqual(heard, []);
      if (gap === "empty") first.answer();
      else if (gap === "canceled") first.abort();
      else first.onerror?.(gap === "refused" ? { error: "supervisor", message: refusal } : { error: "no-speech" });
      assert.deepEqual(heard, ["and merge it"]);
      if (gap === "refused") assert.equal(problems.at(-1), refusal);
      else if (gap === "silence") assert.equal(problems.at(-1), "Nothing was heard.");
      else assert.deepEqual(problems.filter(Boolean), []);
      first.answer("late first words");
      second.answer("duplicate later words");
      assert.deepEqual(heard, ["and merge it"], "a gap drains later words once");
    } finally {
      subject.dispose();
    }
  }
});

test("a recognition deadline aborts the stalled dictation and releases later words once", (context) => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  const { subject, heard, listening, problems } = dictation(SlowRecognizer);
  const timeout = "Dictation did not finish within 120 seconds. Its words were not typed.";
  try {
    subject.start();
    const first = FakeRecognizer.made[0];
    assert.ok(first instanceof SlowRecognizer);
    context.mock.timers.tick(150_000);
    assert.equal(first.abortCount, 0, "time spent recording is outside the recognition deadline");
    subject.stop();
    context.mock.timers.tick(110_000);
    subject.start();
    subject.stop();
    const second = FakeRecognizer.made[1];
    assert.ok(second instanceof SlowRecognizer);
    second.answer("then\nmerge it");
    subject.start();
    const current = FakeRecognizer.made[2];
    assert.ok(current instanceof SlowRecognizer);
    context.mock.timers.tick(9_999);
    assert.equal(first.abortCount, 0);
    assert.deepEqual(heard, []);
    context.mock.timers.tick(1);
    assert.equal(first.abortCount, 1);
    assert.equal(second.abortCount, 0, "a finished later reply is retained while it waits");
    assert.deepEqual(heard, ["then merge it"]);
    assert.equal(problems.at(-1), timeout);
    assert.equal(listening.at(-1), true, "an earlier timeout never closes the current capture");
    first.answer("too late");
    second.answer("duplicate");
    assert.deepEqual(heard, ["then merge it"]);
    current.hear("current\ncapture");
    subject.stop();
    current.answer();
    assert.deepEqual(heard, ["then merge it", "current capture"]);

    subject.start();
    subject.stop();
    const earlier = FakeRecognizer.made[3];
    assert.ok(earlier instanceof SlowRecognizer);
    context.mock.timers.tick(10_000);
    subject.start();
    subject.stop();
    const later = FakeRecognizer.made[4];
    assert.ok(later instanceof SlowRecognizer);
    subject.start();
    subject.stop();
    const last = FakeRecognizer.made[5];
    assert.ok(last instanceof SlowRecognizer);
    last.answer("after both gaps");
    context.mock.timers.tick(109_999);
    assert.equal(earlier.abortCount, 0);
    context.mock.timers.tick(1);
    assert.equal(earlier.abortCount, 1);
    assert.equal(later.abortCount, 0);
    assert.deepEqual(heard, ["then merge it", "current capture"]);
    context.mock.timers.tick(9_999);
    assert.equal(later.abortCount, 0);
    context.mock.timers.tick(1);
    assert.equal(later.abortCount, 1, "the next deadline runs from its own release, without restarting at the queue front");
    assert.deepEqual(heard, ["then merge it", "current capture", "after both gaps"]);
    assert.equal(problems.filter((note) => note === timeout).length, 3);
    earlier.answer("late earlier");
    later.answer("late later");
    assert.deepEqual(heard, ["then merge it", "current capture", "after both gaps"]);
  } finally {
    subject.dispose();
    context.mock.timers.reset();
  }
});

test("closing the terminal drops queued words and cancels recognition", () => {
  const { subject, heard, problems } = dictation(SlowRecognizer);
  subject.start();
  subject.stop();
  subject.start();
  subject.stop();
  const [first, second] = FakeRecognizer.made;
  assert.ok(first instanceof SlowRecognizer);
  assert.ok(second instanceof SlowRecognizer);
  second.answer("buffered words");
  assert.deepEqual(heard, []);
  subject.start();
  const current = FakeRecognizer.made[2];
  assert.ok(current instanceof SlowRecognizer);
  current.hear("unfinished words");
  subject.dispose();
  assert.equal(first.abortCount, 1);
  assert.equal(second.abortCount, 0);
  assert.equal(current.abortCount, 1);
  subject.dispose();
  first.answer("late first");
  second.answer("late second");
  current.answer("late current");
  assert.deepEqual(heard, []);
  assert.deepEqual(problems.filter(Boolean), []);
  assert.equal(first.abortCount, 1);
  assert.equal(current.abortCount, 1);
});

test("dictation queues belong to separate terminals", () => {
  const firstPane = dictation(SlowRecognizer);
  const secondPane = dictation(SlowRecognizer);
  try {
    firstPane.subject.start();
    firstPane.subject.stop();
    firstPane.subject.start();
    firstPane.subject.stop();
    const [first, second] = FakeRecognizer.made;
    assert.ok(first instanceof SlowRecognizer);
    assert.ok(second instanceof SlowRecognizer);
    second.answer("later in the first pane");
    secondPane.subject.start();
    secondPane.subject.stop();
    const other = FakeRecognizer.made[2];
    assert.ok(other instanceof SlowRecognizer);
    other.answer("independent pane");
    assert.deepEqual(firstPane.heard, []);
    assert.deepEqual(secondPane.heard, ["independent pane"]);
    first.answer("earlier in the first pane");
    assert.deepEqual(firstPane.heard, ["earlier in the first pane", "later in the first pane"]);
    assert.deepEqual(secondPane.heard, ["independent pane"]);
  } finally {
    firstPane.subject.dispose();
    secondPane.subject.dispose();
  }
});

test("closing the terminal drops words that are still being recognised", async () => {
  const { subject, mic, heard } = listened(SlowRecognizer);
  subject.start();
  mic.allow();
  await settle();
  subject.stop();
  subject.dispose();
  (FakeRecognizer.made[0] as SlowRecognizer).answer("too late");
  assert.deepEqual(heard, []);
});

test("releasing the keys before the microphone opens starts nothing and keeps nothing open", async () => {
  const { subject, mic, heard, listening } = listened();
  subject.start();
  subject.stop();
  assert.deepEqual(listening, [false]);
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
  assert.deepEqual(listening, [false]);
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
  assert.deepEqual(listening, []);
});

test("a recognizer that fails to start never invites speech", async () => {
  class FailedRecognizer extends TrackRecognizer {
    start() { this.onerror?.({ error: "audio-capture" }); this.onend?.(); }
  }
  const { subject, mic, listening, heard, problems } = listened(FailedRecognizer);
  subject.start();
  mic.allow();
  await settle();
  assert.deepEqual(listening, [false]);
  assert.equal(mic.opened[0].closed, true);
  assert.deepEqual(heard, []);
  assert.match(problems.at(-1) || "", /No microphone/);
});
