import { test } from "node:test";
import assert from "node:assert/strict";
import { DictationSetUp, downsample, isSpoken, localRecognizer, Pieces, wav, type Recording, type Sound } from "./localDictation.ts";
import { Dictation, type Recognizer } from "./dictation.ts";

test("a sound is written as a 16-bit mono WAV at its own rate", () => {
  const bytes = wav({ samples: new Float32Array([0, .5, -.5, 1, -1, 2, -2]), rate: 48000 });
  const view = new DataView(bytes.buffer);
  const tag = (at: number) => String.fromCharCode(...bytes.subarray(at, at + 4));
  assert.equal(bytes.length, 44 + 7 * 2);
  assert.deepEqual([tag(0), tag(8), tag(12), tag(36)], ["RIFF", "WAVE", "fmt ", "data"]);
  assert.equal(view.getUint32(4, true), 36 + 14, "the RIFF size is the file less its first eight bytes");
  assert.deepEqual([view.getUint16(20, true), view.getUint16(22, true), view.getUint32(24, true), view.getUint32(28, true), view.getUint16(32, true), view.getUint16(34, true)], [1, 1, 48000, 96000, 2, 16], "PCM, one channel, the rate, bytes a second, bytes a sample, bits");
  assert.equal(view.getUint32(40, true), 14);
  const samples = Array.from({ length: 7 }, (_, index) => view.getInt16(44 + index * 2, true));
  assert.deepEqual(samples, [0, 16384, -16384, 32767, -32768, 32767, -32768], "louder than full scale is held at full scale");
});

test("a recording is brought down to the model's rate by averaging each run of samples", () => {
  const recorded = { samples: new Float32Array([0, .3, .6, .9, .9, .9, -.3, -.6, -.9, 1]), rate: 48000 };
  const sound = downsample(recorded, 16000);
  assert.equal(sound.rate, 16000);
  assert.deepEqual(Array.from(sound.samples, (sample) => Math.round(sample * 100) / 100), [.3, .9, -.6], "each three samples at 48 kHz are one at 16 kHz, and a part-run left over is dropped");
});

test("a rate that is not a multiple of the model's is averaged over runs of its own length", () => {
  const recorded = { samples: Float32Array.from({ length: 441 }, (_, index) => index), rate: 44100 };
  const sound = downsample(recorded, 16000);
  assert.equal(sound.rate, 16000);
  assert.equal(sound.samples.length, 160, "441 samples at 44.1 kHz are 10 ms, 160 samples at 16 kHz");
  assert.ok(sound.samples.every((sample, index) => index === 0 || sample > sound.samples[index - 1]), "a rising line stays rising");
});

test("a recording at or under the model's rate is kept as it is", () => {
  const recorded = { samples: new Float32Array([.1, .2, .3]), rate: 16000 };
  assert.equal(downsample(recorded, 16000), recorded);
  const lower = { samples: new Float32Array([.1, .2]), rate: 8000 };
  assert.equal(downsample(lower, 16000), lower, "the engine takes a lower rate as it is");
});

// A sound of speech and quiet runs at rate: speech moves like a voice at
// about -9 dB, and quiet is a room's hiss at about -63 dB, under the -40 dB
// a voice reaches.
function said(rate: number, ...parts: ["speech" | "quiet", number][]): Sound {
  const samples: number[] = [];
  for (const [kind, seconds] of parts) {
    for (let i = 0; i < Math.round(seconds * rate); i++) {
      const at = samples.length;
      samples.push(kind === "speech" ? .5 * Math.sin(at * .05) * (1 + .3 * Math.sin(at * .0007)) : .001 * Math.sin(at * 7.3));
    }
  }
  return { samples: Float32Array.from(samples), rate };
}

// cutUp feeds a recording to Pieces as the capture worklet does, 2048
// samples at a time, and returns each piece handed on, when it was handed
// on, and the rest.
function cutUp(sound: Sound) {
  const handed: { piece: Sound; after: number }[] = [];
  let fed = 0;
  const pieces = new Pieces(sound.rate, (piece) => handed.push({ piece, after: fed / sound.rate }));
  for (let at = 0; at < sound.samples.length; at += 2048) {
    const chunk = sound.samples.slice(at, at + 2048);
    fed += chunk.length;
    pieces.push(chunk);
  }
  return { handed, rest: pieces.rest() };
}

function whole(sounds: Sound[]): Float32Array {
  const samples = new Float32Array(sounds.reduce((length, sound) => length + sound.samples.length, 0));
  let at = 0;
  for (const sound of sounds) { samples.set(sound.samples, at); at += sound.samples.length; }
  return samples;
}

test("a long recording is handed on in pieces as it is said, each a phrase or more ending in a pause, with nothing lost or added", () => {
  const phrases: ["speech" | "quiet", number][] = [];
  for (let i = 0; i < 12; i++) phrases.push(["speech", 2 + (i % 3)], ["quiet", i % 4 === 3 ? 2.5 : .5]);
  const sound = said(48000, ...phrases);
  const { handed, rest } = cutUp(sound);
  assert.ok(handed.length >= 5, `${handed.length} pieces of ${sound.samples.length / 48000} s`);
  assert.deepEqual(whole([...handed.map(({ piece }) => piece), rest]), sound.samples, "the pieces and the rest are the recording, in order");
  let end = 0;
  for (const { piece, after } of handed) {
    end += piece.samples.length / 48000;
    assert.ok(piece.samples.length >= 4 * 48000, "a piece is four seconds or more");
    assert.ok(after - end < .05, `a piece ending at ${end} s was handed on at ${after} s, as soon as it was said`);
    assert.ok(isSpoken(piece));
    assert.ok(!isSpoken({ samples: piece.samples.subarray(piece.samples.length - .3 * 48000), rate: 48000 }), "a piece ends in at least 0.3 s of quiet");
  }
});

test("a pause in the first four seconds of a piece does not end it", () => {
  const { handed, rest } = cutUp(said(16000, ["speech", 1], ["quiet", .5], ["speech", 1], ["quiet", .5], ["speech", .5]));
  assert.equal(handed.length, 0);
  assert.equal(rest.samples.length, 3.5 * 16000);
});

test("a recording with no pause, as in a noisy room, is cut at twenty seconds where it was quietest", () => {
  const sound = said(16000, ["speech", 12], ["quiet", .15], ["speech", 30]);
  const { handed, rest } = cutUp(sound);
  assert.ok(handed.length >= 2);
  const first = handed[0].piece.samples.length / 16000;
  assert.ok(first >= 12 && first <= 12.15, `the first piece ends at ${first} s, inside the one dip`);
  for (const { piece } of handed) assert.ok(piece.samples.length <= 20 * 16000);
  assert.deepEqual(whole([...handed.map(({ piece }) => piece), rest]), sound.samples);
});

test("whether any 10 ms of a sound is loud", () => {
  assert.equal(isSpoken(said(16000, ["quiet", 3])), false, "a room's hiss is not speech");
  assert.equal(isSpoken(said(16000, ["quiet", 3], ["speech", .02])), true);
  assert.equal(isSpoken({ samples: new Float32Array([.9, .9]), rate: 16000 }), false, "less than 10 ms is no part at all");
});

const settle = () => new Promise((resolve) => setImmediate(resolve));
const track = { kind: "audio" } as MediaStreamTrack;
const SHORT = { samples: new Float32Array([0, .5, -.5, 1]), rate: 16000 };

// A recorder that hands on the pieces a test says and then the rest it is
// told to, and a supervisor that answers each sound posted, oldest first,
// with the words it is told to or refuses. Its recording runs at once, or
// with late only when the test says it began or could not.
function engine({ sound = SHORT as Sound, words = "open the pull request", refusal = "", late = false } = {}) {
  const recorded: { track: MediaStreamTrack; stopped: boolean; cancelled: boolean; say: (sound: Sound) => void; begin: () => void; fail: (error: Error) => void }[] = [];
  const posted: Uint8Array<ArrayBuffer>[] = [];
  const waiting: { resolve: (text: string) => void; reject: (error: Error) => void }[] = [];
  const open = (from: MediaStreamTrack, piece: (sound: Sound) => void): Recording => {
    let begin = () => {}, fail: (error: Error) => void = () => {};
    const began = late ? new Promise<void>((resolve, reject) => { begin = resolve; fail = reject; }) : Promise.resolve();
    const recording = { track: from, stopped: false, cancelled: false, say: piece, begin, fail };
    recorded.push(recording);
    return { began, stop: async () => { recording.stopped = true; return sound; }, cancel: () => { recording.cancelled = true; } };
  };
  const recognise = (bytes: Uint8Array<ArrayBuffer>) => new Promise<string>((resolve, reject) => {
    posted.push(bytes);
    waiting.push({ resolve, reject });
  });
  const answer = (text = words) => {
    const next = waiting.shift();
    if (refusal) next?.reject(new Error(refusal));
    else next?.resolve(text);
  };
  let warmed = 0;
  return { Recognition: localRecognizer(open, recognise, () => { warmed++; }), recorded, posted, answer, warmed: () => warmed };
}

// samplesOf reads back the samples of a WAV the supervisor was handed.
function samplesOf(bytes: Uint8Array<ArrayBuffer>): number {
  return (bytes.length - 44) / 2;
}

function listen(recognizer: Recognizer) {
  const heard: string[] = [], errors: { error: string; message?: string }[] = [];
  let ended = 0, started = 0;
  recognizer.onstart = () => { started++; };
  recognizer.onresult = (event) => { for (let i = event.resultIndex; i < event.results.length; i++) heard.push(event.results[i][0].transcript); };
  recognizer.onerror = (event) => errors.push(event);
  recognizer.onend = () => { ended++; };
  return { heard, errors, ended: () => ended, started: () => started };
}

test("what is said between start and stop is recorded from the track and its words are delivered once", async () => {
  const { Recognition, recorded, posted, answer } = engine();
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  await settle();
  assert.equal(recorded.length, 1);
  assert.equal(recorded[0].track, track, "the recording is of the track it was handed, not a second microphone");
  recognizer.stop();
  await settle();
  assert.equal(recorded[0].stopped, true);
  assert.equal(posted.length, 1);
  assert.equal(String.fromCharCode(...posted[0].subarray(0, 4)), "RIFF", "the supervisor is handed a WAV");
  assert.deepEqual(events.heard, [], "nothing is delivered before the supervisor answers");
  answer();
  await settle();
  assert.deepEqual(events.heard, ["open the pull request"]);
  assert.deepEqual(events.errors, []);
  assert.equal(events.ended(), 1);
  recognizer.stop();
  await settle();
  assert.equal(posted.length, 1, "a second stop asks nothing again");
});

test("each piece said while the keys are held is recognised at once, one at a time in order, and every piece's words are typed together, in order, at release", async () => {
  const rest = said(16000, ["speech", 3], ["quiet", .4]);
  const { Recognition, recorded, posted, answer } = engine({ sound: rest });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recorded[0].say(said(16000, ["speech", 5], ["quiet", .3]));
  recorded[0].say(said(16000, ["speech", 6], ["quiet", .3]));
  await settle();
  assert.equal(posted.length, 1, "the first piece is asked for while the keys are still held");
  assert.equal(samplesOf(posted[0]), 5.3 * 16000);
  answer("First, the board.");
  await settle();
  assert.equal(posted.length, 2, "the next piece is asked for once the first is answered");
  answer("Then the voice.");
  await settle();
  assert.deepEqual(events.heard, [], "nothing is typed before the keys are let go");
  recognizer.stop();
  await settle();
  assert.equal(posted.length, 3);
  assert.equal(samplesOf(posted[2]), 3.4 * 16000, "at release only what followed the last piece waits");
  answer("And the rest.");
  await settle();
  assert.deepEqual(events.heard, ["First, the board. Then the voice. And the rest."]);
  assert.equal(events.ended(), 1);
});

test("room noise after the last piece, or a piece of it alone, is never sent, as the model writes words of its own into it", async () => {
  const { Recognition, recorded, posted, answer } = engine({ sound: said(16000, ["quiet", 6]) });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recorded[0].say(said(16000, ["speech", 5], ["quiet", .3]));
  recorded[0].say(said(16000, ["quiet", 20]));
  await settle();
  answer();
  recognizer.stop();
  await settle();
  assert.equal(posted.length, 1);
  assert.deepEqual(events.heard, ["open the pull request"]);
});

test("a dictation with nothing loud in it, as from a quiet microphone, is sent whole, as it is", async () => {
  const { Recognition, recorded, posted, answer } = engine({ sound: said(16000, ["quiet", 3]) });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recorded[0].say(said(16000, ["quiet", 20]));
  recognizer.stop();
  await settle();
  assert.equal(posted.length, 1);
  assert.equal(samplesOf(posted[0]), 23 * 16000);
  answer();
  await settle();
  assert.deepEqual(events.heard, ["open the pull request"]);
});

test("a piece whose words could not be had is asked for again, its sound kept, and the dictation is typed whole", async (context) => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  let refusals = 2;
  const posted: Uint8Array<ArrayBuffer>[] = [];
  const Recognition = localRecognizer(
    () => ({ began: Promise.resolve(), stop: async () => said(16000, ["speech", 2]), cancel: () => {} }),
    async (bytes) => { posted.push(bytes); if (refusals-- > 0) throw new Error("the dictation engine failed: EOF"); return "every word"; },
    () => {},
  );
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recognizer.stop();
  await settle();
  assert.equal(posted.length, 1);
  context.mock.timers.tick(499);
  await settle();
  assert.equal(posted.length, 1, "it waits half a second before asking again");
  context.mock.timers.tick(1);
  await settle();
  assert.equal(posted.length, 2);
  context.mock.timers.tick(2000);
  await settle();
  assert.equal(posted.length, 3);
  assert.deepEqual(posted[2], posted[0], "the same sound is asked for again");
  assert.deepEqual(events.heard, ["every word"]);
  assert.deepEqual(events.errors, []);
});

test("the supervisor's refusal is passed on in its own words once asking again has not helped", async (context) => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  const refusal = "Dictation needs 1 GB of free memory and 1 GB of free commit, and this PC has 0.7 GB and 3.0 GB.";
  const { Recognition, posted, answer } = engine({ refusal });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recognizer.stop();
  await settle();
  for (const wait of [500, 2000]) {
    answer();
    await settle();
    assert.deepEqual(events.errors, []);
    context.mock.timers.tick(wait);
    await settle();
  }
  answer();
  await settle();
  assert.equal(posted.length, 3);
  assert.deepEqual(events.heard, []);
  assert.deepEqual(events.errors, [{ error: "supervisor", message: refusal }]);
  assert.equal(events.ended(), 1);
});

test("a refusal while the speech model is being set up is passed on at once, for the board to show how far the set-up is", async () => {
  const note = "Dictation is being set up, once: downloading its speech model, 12 of 28 MB, which stays on this PC. Dictate again when it is ready.";
  const posted: Uint8Array<ArrayBuffer>[] = [];
  const Recognition = localRecognizer(
    () => ({ began: Promise.resolve(), stop: async () => said(16000, ["speech", 2]), cancel: () => {} }),
    async (bytes) => { posted.push(bytes); throw new DictationSetUp(note); },
    () => {},
  );
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recognizer.stop();
  await settle();
  assert.equal(posted.length, 1, "asking again would not set the model up sooner");
  assert.deepEqual(events.errors, [{ error: "setup", message: note }]);
});

test("speech the engine found no words in is unheard, never nothing heard", async () => {
  const { Recognition, recorded, answer } = engine({ sound: said(16000, ["speech", 2]), words: "" });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recorded[0].say(said(16000, ["speech", 60], ["quiet", .3]));
  await settle();
  answer();
  recognizer.stop();
  await settle();
  answer();
  await settle();
  assert.deepEqual(events.heard, []);
  assert.deepEqual(events.errors, [{ error: "unheard", message: "The engine answered no words for 62 s of dictation with speech in it." }]);
});

test("the engine is warmed as the recording begins, once a dictation, so its words come soon after the keys are let go", async () => {
  const { Recognition, warmed, answer } = engine();
  const recognizer = new Recognition();
  listen(recognizer);
  assert.equal(warmed(), 0, "nothing is warmed before a dictation begins");
  recognizer.start(track);
  assert.equal(warmed(), 1, "the engine is warmed while the words are still being said");
  recognizer.stop();
  await settle();
  answer();
  await settle();
  assert.equal(warmed(), 1, "letting go warms nothing more");
});

test("a dictation with no track to record warms nothing", () => {
  const { Recognition, warmed } = engine();
  const recognizer = new Recognition();
  listen(recognizer);
  recognizer.start();
  assert.equal(warmed(), 0);
});

test("a recording with no sound in it asks the supervisor nothing and says nothing was heard", async () => {
  const { Recognition, posted } = engine({ sound: { samples: new Float32Array(0), rate: 16000 } });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recognizer.stop();
  await settle();
  assert.equal(posted.length, 0);
  assert.deepEqual(events.errors, [{ error: "no-speech" }]);
  assert.equal(events.ended(), 1);
});

test("no words for a sound with nothing loud in it is nothing heard, not an empty line", async () => {
  const { Recognition, answer } = engine({ words: "" });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recognizer.stop();
  await settle();
  answer();
  await settle();
  assert.deepEqual(events.heard, []);
  assert.deepEqual(events.errors, [{ error: "no-speech" }]);
});

test("aborting drops the recording, and an answer that arrives later is not delivered", async () => {
  const { Recognition, recorded } = engine();
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  await settle();
  recognizer.abort();
  await settle();
  assert.equal(recorded[0].cancelled, true);
  assert.deepEqual(events.errors, [{ error: "aborted" }]);
  assert.equal(events.ended(), 1);

  const late = engine();
  const second = new late.Recognition();
  const lateEvents = listen(second);
  second.start(track);
  second.stop();
  await settle();
  second.abort();
  late.answer();
  await settle();
  assert.deepEqual(lateEvents.heard, []);
  assert.equal(lateEvents.ended(), 1, "the end is told once");
});

test("aborting while the recording stops never asks the supervisor", async () => {
  const posts: Uint8Array<ArrayBuffer>[] = [];
  const Recognition = localRecognizer(
    () => ({ began: Promise.resolve(), stop: async () => SHORT, cancel: () => {} }),
    async (bytes) => { posts.push(bytes); return "too late"; },
    () => {},
  );
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recognizer.stop();
  recognizer.abort();
  await settle();
  assert.equal(posts.length, 0, "a recording that stops after cancellation must never POST");
  assert.deepEqual(events.heard, []);
  assert.deepEqual(events.errors, [{ error: "aborted" }]);
  assert.equal(events.ended(), 1);
});

test("aborting a pending recognition cancels its HTTP request, asks no more and ignores late words", async (context) => {
  let requests = 0;
  let is_request_aborted = false;
  let answer: () => void = () => { throw new Error("No request arrived"); };
  context.mock.method(globalThis, "fetch", (input: Parameters<typeof fetch>[0], init?: RequestInit) => {
    assert.equal(input, "http://127.0.0.1:1/api/dictation");
    assert.equal(init?.method, "POST");
    requests++;
    return new Promise<Response>((resolve, reject) => {
      answer = () => resolve(new Response("too late"));
      init?.signal?.addEventListener("abort", () => {
        is_request_aborted = true;
        reject(new DOMException("The request was aborted", "AbortError"));
      }, { once: true });
    });
  });
  const Recognition = localRecognizer(
    () => ({ began: Promise.resolve(), stop: async () => SHORT, cancel: () => {} }),
    (bytes, signal?: AbortSignal) => fetch("http://127.0.0.1:1/api/dictation", { method: "POST", body: bytes, signal }).then((response) => response.text()),
    () => {},
  );
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recognizer.stop();
  await settle();
  assert.equal(requests, 1);
  recognizer.abort();
  answer();
  await settle();
  await settle();
  assert.equal(is_request_aborted, true, "abort must reach the in-flight HTTP request");
  assert.deepEqual(events.heard, []);
  assert.deepEqual(events.errors, [{ error: "aborted" }]);
  assert.equal(events.ended(), 1);
  recognizer.stop();
  assert.equal(requests, 1, "cancellation never posts the sound again");
});

test("without a track there is nothing to record", () => {
  const { Recognition, recorded } = engine();
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start();
  assert.equal(recorded.length, 0);
  assert.deepEqual(events.errors, [{ error: "audio-capture" }]);
  assert.equal(events.ended(), 1);
});

test("dictation types what the local engine heard, and a refusal goes to the CFO, never onto the board", async (context) => {
  context.mock.timers.enable({ apis: ["setTimeout"] });
  const { Recognition, answer } = engine();
  const heard: string[] = [], notes: string[] = [], failures: string[] = [], listening: boolean[] = [];
  let closed = 0;
  const capture = { track, level: () => .4, close: () => { closed++; } };
  const events = { heard: (text: string) => heard.push(text), listening: (on: boolean) => listening.push(on), problem: (note: string) => notes.push(note), failed: (reason: string) => failures.push(reason) };
  const subject = new Dictation(events, () => Recognition, "en-GB", async () => capture);
  subject.start();
  await settle();
  subject.stop();
  await settle();
  assert.equal(closed, 1, "the microphone closes at release, before the supervisor answers");
  assert.deepEqual(listening, [true, false]);
  answer();
  await settle();
  assert.deepEqual(heard, ["open the pull request"]);
  assert.deepEqual(listening, [true, false]);

  const refusal = "The dictation engine failed: EOF.";
  const refused = engine({ refusal });
  const second = new Dictation({ ...events, listening: () => {} }, () => refused.Recognition, "en-GB", async () => capture);
  second.start();
  await settle();
  second.stop();
  await settle();
  for (const wait of [500, 2000]) {
    refused.answer();
    await settle();
    context.mock.timers.tick(wait);
    await settle();
  }
  refused.answer();
  await settle();
  assert.deepEqual(failures, [refusal]);
  assert.deepEqual(notes.filter(Boolean), []);
  assert.deepEqual(heard, ["open the pull request"]);
});

test("the recognizer says it listens once its recording runs, not when it is handed the track", async () => {
  // Arrange
  const { Recognition, recorded } = engine({ late: true });
  const recognizer = new Recognition();
  const events = listen(recognizer);

  // Act
  recognizer.start(track);
  await settle();

  // Assert: on a busy PC the recording takes a while to get under way.
  assert.equal(events.started(), 0, "nothing is recorded yet, so nothing listens");
  recorded[0].begin();
  await settle();
  assert.equal(events.started(), 1);
});

test("a recording that cannot begin goes to the CFO at once and never listens", async () => {
  // Arrange
  const { Recognition, recorded, posted } = engine({ late: true });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);

  // Act
  recorded[0].fail(new Error("The capture worklet could not be loaded."));
  await settle();

  // Assert
  assert.equal(events.started(), 0);
  assert.deepEqual(events.errors, [{ error: "supervisor", message: "The capture worklet could not be loaded." }]);
  assert.equal(events.ended(), 1);
  assert.equal(recorded[0].cancelled, true);
  assert.equal(posted.length, 0);
});

test("a hold let go before the recording ran asks the supervisor nothing and says nothing, and the next one that records is typed", async () => {
  // Arrange: the Overlord's short hold on a busy PC, whose recording had not
  // begun when he let go, though the microphone was open.
  const { Recognition, recorded, posted, answer } = engine({ late: true, sound: said(16000, ["speech", 1]) });
  const heard: string[] = [], notes: string[] = [], failures: string[] = [], listening: boolean[] = [];
  const capture = { track, level: () => .4, close: () => {} };
  const subject = new Dictation({ heard: (text) => heard.push(text), listening: (on) => listening.push(on), problem: (note) => notes.push(note), failed: (reason) => failures.push(reason) }, () => Recognition, "en-GB", async () => capture);

  // Act
  subject.start();
  await settle();
  assert.deepEqual(listening, [], "the bubble does not say it listens before anything records");
  subject.stop();
  await settle();

  // Assert
  assert.equal(recorded[0].cancelled, true, "the recording is dropped");
  assert.equal(posted.length, 0);
  assert.deepEqual(notes.filter(Boolean), [], "nothing listened, so it is not told as nothing heard");
  assert.deepEqual(failures, []);

  // Act: the next hold is kept until the bubble listens.
  subject.start();
  await settle();
  recorded[1].begin();
  await settle();
  assert.deepEqual(listening, [false, true]);
  subject.stop();
  await settle();
  answer();
  await settle();

  // Assert
  assert.deepEqual(heard, ["open the pull request"]);
  assert.deepEqual(notes.filter(Boolean), []);
});

test("a hold with nothing said in it, recorded and answered with no words, says Nothing was heard once", async () => {
  // Arrange
  const { Recognition, answer } = engine({ sound: said(16000, ["quiet", 2]), words: "" });
  const heard: string[] = [], notes: string[] = [], failures: string[] = [];
  const capture = { track, level: () => 0, close: () => {} };
  const subject = new Dictation({ heard: (text) => heard.push(text), listening: () => {}, problem: (note) => notes.push(note), failed: (reason) => failures.push(reason) }, () => Recognition, "en-GB", async () => capture);

  // Act
  subject.start();
  await settle();
  subject.stop();
  await settle();
  answer();
  await settle();

  // Assert
  assert.deepEqual(notes.filter(Boolean), ["Nothing was heard."]);
  assert.deepEqual(heard, []);
  assert.deepEqual(failures, []);
});
