import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The app's push worker as its browser runs it, with what it does kept, and
// as many boards open as boards says.
function runWorker(boards = 0) {
  const listeners: Record<string, (event: object) => void> = {};
  const done: string[] = [];
  const board = (index: number) => ({
    focus: async () => { done.push(`focus board ${index}`); },
    postMessage: (message: unknown) => { done.push(`tell board ${index} ${JSON.stringify(message)}`); },
  });
  const self = {
    addEventListener: (type: string, listener: (event: object) => void) => { listeners[type] = listener; },
    navigator: { setAppBadge: async (count: number) => { done.push("badge " + count); }, clearAppBadge: async () => { done.push("badge cleared"); } },
    registration: { showNotification: async (title: string, options: { body: string; tag?: string }) => { done.push(`notify ${title}: ${options.body} (${options.tag})`); } },
    clients: { matchAll: async () => Array.from({ length: boards }, (_, index) => board(index)), openWindow: async (url: string) => { done.push("open " + url); } },
  };
  new Function("self", readFileSync(new URL("../public/push-worker.js", import.meta.url), "utf8"))(self);
  const fire = async (type: string, event: object) => {
    let waited: Promise<unknown> = Promise.resolve();
    listeners[type]({ ...event, waitUntil: (promise: Promise<unknown>) => { waited = promise; } });
    await waited;
  };
  return { done, fire };
}

// A push from the supervisor shows its item and puts how many wait on the
// app's icon, and takes the count off at none.
for (const [name, push, want] of [
  ["an item", { title: "Gordy", body: "Gordy wants your review: The release notes", count: 2, tag: "review:r1@T" }, ["badge 2", "notify Gordy: Gordy wants your review: The release notes (review:r1@T)"]],
  ["none waiting", { title: "CEO", body: "The CEO asks: Ship it?", count: 0, tag: "question:q1@T" }, ["badge cleared", "notify CEO: The CEO asks: Ship it? (question:q1@T)"]],
  ["no payload", null, ["badge cleared", "notify Code Goblins:  (undefined)"]],
] as const) {
  test(`the app's push worker on ${name}`, async () => {
    // Arrange
    const worker = runWorker();

    // Act
    await worker.fire("push", { data: push && { json: () => push } });

    // Assert
    assert.deepEqual([...worker.done].sort(), [...want].sort());
  });
}

// Tapping a push's notification opens its item (#2363): in a board already
// open, which it brings up and tells the item's address, so what he was
// typing there stays, or else in a board it opens at that address. A push
// that names no item opens the board as it is.
for (const [name, tag, boards, want] of [
  ["an item, with no board open", "review:r1@2026-11-12T17:00:00Z", 0, ["open /?item=review%3Ar1"]],
  ["an item, with a board open", "question:q1@T", 1, ['tell board 0 {"open":"/?item=question%3Aq1"}', "focus board 0"]],
  ["an item, with two boards open", "question:q1@T", 2, ['tell board 0 {"open":"/?item=question%3Aq1"}', "focus board 0"]],
  ["no item, with no board open", "", 0, ["open /"]],
  ["no item, with a board open", "", 1, ["focus board 0"]],
] as const) {
  test(`tapping a push's notification for ${name}`, async () => {
    // Arrange
    const worker = runWorker(boards);
    let closed = false;

    // Act
    await worker.fire("notificationclick", { notification: { tag, close: () => { closed = true; } } });

    // Assert
    assert.equal(closed, true);
    assert.deepEqual(worker.done, want);
  });
}
