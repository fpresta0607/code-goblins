import { test } from "node:test";
import assert from "node:assert/strict";
import { bannerItems, countedTitle } from "./arrivals.ts";
import type { Item } from "./commandQueue.ts";
import type { Question, Review, Run } from "./types.ts";

const question = (id: string): Item => ({ kind: "question", key: "question:" + id, question: { id } as Question });
const review = (id: string): Item => ({ kind: "review", key: "review:" + id, review: { id } as Review });
const run = (id: string): Item => ({ kind: "run", key: "run:" + id, run: { id } as Run });

test("a new review item or command is announced once; a question opens the Command Center instead", () => {
  const waiting = [question("q1"), review("plan"), run("restart")];
  assert.deepEqual(bannerItems(waiting, new Set()).map((item) => item.key), ["review:plan", "run:restart"]);
  assert.deepEqual(bannerItems(waiting, new Set(["review:plan"])).map((item) => item.key), ["run:restart"], "an item already announced is not announced again");
  assert.deepEqual(bannerItems([], new Set()), []);
});

test("the tab's title carries how many items wait on the Overlord", () => {
  assert.equal(countedTitle("Code Goblins", 3), "(3) Code Goblins");
  assert.equal(countedTitle("Code Goblins", 0), "Code Goblins");
});
