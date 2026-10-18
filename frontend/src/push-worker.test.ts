import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The app's push worker as its browser runs it, with what it does kept.
function runWorker() {
  const listeners: Record<string, (event: object) => void> = {};
  const done: string[] = [];
  const self = {
    addEventListener: (type: string, listener: (event: object) => void) => { listeners[type] = listener; },
    navigator: { setAppBadge: async (count: number) => { done.push("badge " + count); }, clearAppBadge: async () => { done.push("badge cleared"); } },
    registration: { showNotification: async (title: string, options: { body: string; tag?: string }) => { done.push(`notify ${title}: ${options.body} (${options.tag})`); } },
    clients: { matchAll: async () => [], openWindow: async (url: string) => { done.push("open " + url); } },
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
  ["none waiting", { title: "CFO", body: "The CFO asks: Ship it?", count: 0, tag: "question:q1@T" }, ["badge cleared", "notify CFO: The CFO asks: Ship it? (question:q1@T)"]],
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

test("tapping a push's notification opens the board", async () => {
  // Arrange
  const worker = runWorker();
  let closed = false;

  // Act
  await worker.fire("notificationclick", { notification: { close: () => { closed = true; } } });

  // Assert
  assert.equal(closed, true);
  assert.deepEqual(worker.done, ["open /"]);
});
