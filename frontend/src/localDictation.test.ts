import { test } from "node:test";
import assert from "node:assert/strict";
import { localRecognizer, wav, type Recording, type Sound } from "./localDictation.ts";
import { Dictation, type Recognizer } from "./dictation.ts";

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const track = { kind: "audio" } as MediaStreamTrack;

// A recorder that hands back the sound it is told to, and a supervisor that
// answers the words it is told to or refuses.
function engine({ sound = { samples: new Float32Array([0, .5, -.5, 1]), rate: 16000 } as Sound, words = "open the pull request", refusal = "" } = {}) {
  const recorded: { track: MediaStreamTrack; stopped: boolean; cancelled: boolean }[] = [];
  const posted: Uint8Array<ArrayBuffer>[] = [];
  let answer: (() => void) | null = null;
  const open = async (from: MediaStreamTrack): Promise<Recording> => {
    const recording = { track: from, stopped: false, cancelled: false };
    recorded.push(recording);
    return { stop: async () => { recording.stopped = true; return sound; }, cancel: () => { recording.cancelled = true; } };
  };
  const recognise = (bytes: Uint8Array<ArrayBuffer>) => new Promise<string>((resolve, reject) => {
    posted.push(bytes);
    answer = () => refusal ? reject(new Error(refusal)) : resolve(words);
  });
  return { Recognition: localRecognizer(open, recognise), recorded, posted, answer: () => answer?.() };
}

function listen(recognizer: Recognizer) {
  const heard: string[] = [], errors: { error: string; message?: string }[] = [];
  let ended = 0;
  recognizer.onresult = (event) => { for (let i = event.resultIndex; i < event.results.length; i++) heard.push(event.results[i][0].transcript); };
  recognizer.onerror = (event) => errors.push(event);
  recognizer.onend = () => { ended++; };
  return { heard, errors, ended: () => ended };
}

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

test("words the model did not find are nothing heard, not an empty line", async () => {
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

test("the supervisor's refusal is passed on in its own words", async () => {
  const refusal = "The speech model is being downloaded, once: 45 of 103 MB. Dictate again when it is there.";
  const { Recognition, answer } = engine({ refusal });
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start(track);
  recognizer.stop();
  await settle();
  answer();
  await settle();
  assert.deepEqual(events.heard, []);
  assert.deepEqual(events.errors, [{ error: "supervisor", message: refusal }]);
  assert.equal(events.ended(), 1);
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

test("without a track there is nothing to record", () => {
  const { Recognition, recorded } = engine();
  const recognizer = new Recognition();
  const events = listen(recognizer);
  recognizer.start();
  assert.equal(recorded.length, 0);
  assert.deepEqual(events.errors, [{ error: "audio-capture" }]);
  assert.equal(events.ended(), 1);
});

test("dictation types what the local engine heard and shows its refusal as it was written", async () => {
  const { Recognition, answer } = engine();
  const heard: string[] = [], notes: string[] = [], listening: boolean[] = [];
  const capture = { track, level: () => .4, close: () => {} };
  const subject = new Dictation({ heard: (text) => heard.push(text), listening: (on) => listening.push(on), problem: (note) => notes.push(note) }, () => Recognition, "en-GB", async () => capture);
  subject.start();
  await settle();
  subject.stop();
  await settle();
  answer();
  await settle();
  assert.deepEqual(heard, ["open the pull request"]);
  assert.deepEqual(listening, [true, false]);

  const refusal = "Dictation needs 1 GB of free memory and 1 GB of free commit, and this PC has 0.7 GB and 3.0 GB.";
  const refused = engine({ refusal });
  const second = new Dictation({ heard: (text) => heard.push(text), listening: () => {}, problem: (note) => notes.push(note) }, () => refused.Recognition, "en-GB", async () => capture);
  second.start();
  await settle();
  second.stop();
  await settle();
  refused.answer();
  await settle();
  assert.equal(notes.at(-1), refusal);
  assert.deepEqual(heard, ["open the pull request"]);
});
