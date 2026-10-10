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

// A recognizer that plays back what the browser's would do: it says it has
// started once it listens, hands on each final phrase, and ends when stopped.
class FakeRecognizer implements Recognizer {
  static made: FakeRecognizer[] = [];
  continuous = false;
  interimResults = true;
  lang = "";
  started = false;
  onstart: Recognizer["onstart"] = null;
  onresult: Recognizer["onresult"] = null;
  onerror: Recognizer["onerror"] = null;
  onend: Recognizer["onend"] = null;
  constructor() { FakeRecognizer.made.push(this); }
  start() { this.started = true; this.onstart?.(); }
  stop() { this.onend?.(); }
  abort() { this.onerror?.({ error: "aborted" }); this.onend?.(); }
  hear(...finals: string[]) {
    const results = finals.map((transcript) => [{ transcript }]);
    this.onresult?.({ resultIndex: 0, results });
  }
  // After a stretch of silence the browser's recognizer gives up by itself:
  // a no-speech error, then its end.
  silence() { this.onerror?.({ error: "no-speech" }); this.onend?.(); }
}

function dictation(recognition: new () => Recognizer = FakeRecognizer) {
  FakeRecognizer.made = [];
  const heard: string[] = [], listening: boolean[] = [], problems: string[] = [], failures: string[] = [];
  const subject = new Dictation({ heard: (text) => heard.push(text), listening: (on) => listening.push(on), problem: (note) => problems.push(note), failed: (reason) => failures.push(reason) }, () => recognition, "en-GB");
  return { subject, heard, listening, problems, failures };
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
  start(track?: unknown) { this.heardFrom = track; this.started = true; this.onstart?.(); }
}

function listened(recognition: new () => Recognizer = TrackRecognizer) {
  FakeRecognizer.made = [];
  const mic = microphone();
  const heard: string[] = [], listening: boolean[] = [], problems: string[] = [];
  const subject = new Dictation({ heard: (text) => heard.push(text), listening: (on) => listening.push(on), problem: (note) => problems.push(note), failed: () => {} }, () => recognition, "en-GB", mic.open);
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

test("an empty or refused earlier dictation releases later words without typing a gap or a note", () => {
  const refusal = "Dictation needs 1 GB of free memory and 1 GB of free commit.";
  for (const gap of ["empty", "refused", "canceled", "silence"]) {
    const { subject, heard, problems, failures } = dictation(SlowRecognizer);
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
      else if (gap === "refused") first.onerror?.({ error: "supervisor", message: refusal });
      else first.silence();
      assert.deepEqual(heard, ["and merge it"]);
      assert.deepEqual(problems.filter(Boolean), [], "a refusal goes to the CFO, and a silence he has dictated past is not told over the words that followed it");
      assert.deepEqual(failures, gap === "refused" ? [refusal] : []);
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
  const { subject, heard, listening, problems, failures } = dictation(SlowRecognizer);
  const timeout = "Dictation did not finish within 120 seconds of the keys being let go, so its words were not typed.";
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
    assert.equal(failures.at(-1), timeout);
    assert.deepEqual(problems.filter(Boolean), [], "a dictation that timed out goes to the CFO, never onto the board");
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
    assert.equal(failures.filter((reason) => reason === timeout).length, 3);
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
    start(track?: unknown) { if (track !== undefined) throw new TypeError("parameter 1 is not of type 'MediaStreamTrack'"); this.started = true; this.onstart?.(); }
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

// The Overlord, 2026-10-09: "there's still some type of random alert, even
// though dictation is working. It'll say nothing was heard when it was in
// fact heard." Each test below is one way a hold could show a note it had not
// earned, with the recognizer's events as the browser and the board's own
// engine send them.

// A recognizer that listens only when told to, as the board's own does while
// its recording is still getting under way on a busy PC.
class LateRecognizer extends SlowRecognizer {
  start(track?: unknown) { this.heardFrom = track; this.started = true; }
  listen() { this.onstart?.(); }
}

test("the bubble says it listens only once the recognizer does, and a hold let go before that is dropped without a note", async () => {
  // Arrange
  const { subject, mic, heard, listening, problems } = listened(LateRecognizer);

  // Act: the keys go down, the microphone opens, and the keys are let go
  // before anything records.
  subject.start();
  mic.allow();
  await settle();
  const early = FakeRecognizer.made[0] as LateRecognizer;
  assert.equal(early.started, true);
  assert.deepEqual(listening, [], "a recognizer handed the track is not yet listening");
  subject.stop();
  early.silence();

  // Assert: nothing listened, so nothing is said of what it heard.
  assert.equal(early.abortCount, 1, "its recording is dropped");
  assert.equal(early.stopped, false, "and no words are asked of it");
  assert.deepEqual(problems.filter(Boolean), []);
  assert.deepEqual(heard, []);
  assert.equal(mic.opened[0].closed, true);

  // Act: the next hold is kept until the recognizer listens.
  subject.start();
  mic.allow();
  await settle();
  const late = FakeRecognizer.made[1] as LateRecognizer;
  assert.deepEqual(listening, [false]);
  late.listen();
  assert.deepEqual(listening, [false, true]);
  subject.stop();
  late.answer("ship it");

  // Assert
  assert.deepEqual(heard, ["ship it"]);
  assert.deepEqual(problems.filter(Boolean), []);
});

test("a recognizer that never comes to listen ends the hold and the CFO is told, never the board", (context) => {
  // Arrange
  context.mock.timers.enable({ apis: ["setTimeout"] });
  const { subject, heard, listening, problems, failures } = dictation(LateRecognizer);
  try {
    subject.start();
    const stuck = FakeRecognizer.made[0] as LateRecognizer;

    // Act: five seconds pass with the microphone open and nothing recording.
    context.mock.timers.tick(4999);
    assert.equal(stuck.abortCount, 0);
    assert.deepEqual(failures, []);
    context.mock.timers.tick(1);

    // Assert: a hold that cannot record is a failure someone hears of.
    assert.equal(stuck.abortCount, 1);
    assert.deepEqual(failures, ["Dictation did not begin recording within 5 seconds of the microphone opening, so nothing was typed."]);
    assert.deepEqual(problems.filter(Boolean), []);
    assert.deepEqual(listening, [false]);
    subject.stop();
    assert.deepEqual(listening, [false], "letting go afterwards changes nothing");

    // Act: a recognizer that listens in time has no such deadline.
    subject.start();
    const next = FakeRecognizer.made[1] as LateRecognizer;
    next.listen();
    context.mock.timers.tick(60_000);

    // Assert
    assert.deepEqual(listening, [false, true]);
    assert.equal(next.abortCount, 0);
    assert.equal(failures.length, 1);
    assert.deepEqual(heard, []);
  } finally {
    subject.dispose();
    context.mock.timers.reset();
  }
});

test("words said before a silence the browser's recognizer gives up over are typed at the release, with no note", (context) => {
  // Arrange
  context.mock.timers.enable({ apis: ["Date"] });
  const { subject, heard, listening, problems } = dictation();
  subject.start();
  const first = FakeRecognizer.made[0];
  first.hear("open the pull request");

  // Act: eight seconds of silence, which Chrome ends its recognizer over.
  context.mock.timers.tick(8000);
  first.silence();

  // Assert: the hold goes on, on a recognizer of its own.
  assert.deepEqual(problems.filter(Boolean), [], "a silence in a hold with words in it is no problem");
  assert.deepEqual(heard, [], "nothing is typed while the keys are held");
  assert.deepEqual(listening, [true], "the bubble keeps listening");
  assert.equal(FakeRecognizer.made.length, 2);
  assert.equal(FakeRecognizer.made[1].started, true);

  // Act
  subject.stop();

  // Assert
  assert.deepEqual(heard, ["open the pull request"]);
  assert.deepEqual(problems.filter(Boolean), []);
  assert.deepEqual(listening, [true, false]);
});

test("a recognizer that ends by itself while the keys are held is started again, and all the hold's words are typed together", (context) => {
  // Arrange
  context.mock.timers.enable({ apis: ["Date"] });
  const { subject, heard, listening, problems } = dictation();
  subject.start();
  const first = FakeRecognizer.made[0];
  first.hear("first the board");

  // Act: the browser ends a long recognition by itself, with no error.
  context.mock.timers.tick(60_000);
  first.onend?.();

  // Assert
  assert.equal(FakeRecognizer.made.length, 2);
  const second = FakeRecognizer.made[1];
  assert.equal(second.started, true);
  assert.deepEqual([second.continuous, second.interimResults, second.lang], [true, false, "en-GB"]);
  assert.deepEqual(heard, []);
  assert.deepEqual(listening, [true], "the bubble never flickers");

  // Act
  second.hear("then the voice");
  subject.stop();

  // Assert
  assert.deepEqual(heard, ["first the board then the voice"]);
  assert.deepEqual(problems.filter(Boolean), []);
  assert.deepEqual(listening, [true, false]);
});

test("a hold in which nothing was said says Nothing was heard once, at the release", (context) => {
  context.mock.timers.enable({ apis: ["Date"] });
  for (const silences of [0, 1, 3]) {
    // Arrange
    const { subject, heard, listening, problems } = dictation();
    subject.start();

    // Act: the browser's recognizer gives up each eight seconds of the hold.
    for (let count = 0; count < silences; count++) {
      context.mock.timers.tick(8000);
      FakeRecognizer.made[count].silence();
    }
    assert.deepEqual(problems.filter(Boolean), [], `nothing is said while the keys are held, after ${silences} silences`);
    assert.deepEqual(listening, [true]);
    subject.stop();

    // Assert
    assert.deepEqual(problems.filter(Boolean), ["Nothing was heard."], `after ${silences} silences`);
    assert.deepEqual(heard, []);
    assert.deepEqual(listening, [true, false]);
  }
});

test("a silent hold he has already dictated past is never told over the next one", () => {
  // Arrange: a hold with nothing said, whose verdict is still on its way.
  const { subject, heard, problems } = dictation(SlowRecognizer);
  subject.start();
  subject.stop();

  // Act: he is dictating again when it arrives.
  subject.start();
  const [silent, next] = FakeRecognizer.made as SlowRecognizer[];
  silent.silence();
  assert.deepEqual(problems.filter(Boolean), [], "the note would read as being about the hold he is in");
  next.hear("and merge it");
  subject.stop();
  next.answer();

  // Assert
  assert.deepEqual(heard, ["and merge it"]);
  assert.deepEqual(problems.filter(Boolean), []);
});

test("a recognizer that gives up as soon as it starts ends the hold instead of being started again", () => {
  // Arrange
  const { subject, heard, listening, problems } = dictation();
  subject.start();

  // Act: no time has passed, so this is a recognizer that cannot listen.
  FakeRecognizer.made[0].silence();

  // Assert
  assert.equal(FakeRecognizer.made.length, 1);
  assert.deepEqual(listening, [true, false]);
  assert.deepEqual(problems.filter(Boolean), ["Nothing was heard."]);
  assert.deepEqual(heard, []);
});

test("a failure that is real still says so once and ends the hold, whatever was heard before it", (context) => {
  context.mock.timers.enable({ apis: ["Date"] });
  for (const [error, says] of [["not-allowed", /microphone is blocked/], ["service-not-allowed", /microphone is blocked/], ["audio-capture", /No microphone/], ["network", /network/]] as const) {
    // Arrange
    const { subject, listening, problems } = dictation();
    subject.start();
    const recognizer = FakeRecognizer.made[0];
    recognizer.hear("half a thought");
    context.mock.timers.tick(8000);

    // Act: the browser reports the failure, then the recognizer's end.
    recognizer.onerror?.({ error });
    recognizer.onend?.();

    // Assert
    assert.equal(problems.filter(Boolean).length, 1, error);
    assert.match(problems.at(-1) || "", says);
    assert.equal(FakeRecognizer.made.length, 1, "a recognizer that failed is not started again");
    assert.deepEqual(listening, [true, false]);
  }
});
