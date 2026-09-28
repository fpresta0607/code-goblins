import { test } from "node:test";
import assert from "node:assert/strict";
import { availableHeight, clampPage, fitKey, pageLabel, pageOf, pageStarts, swipeStep } from "./fit.ts";

test("a page holds as many of its own cards as fit, so a tall card shortens only its own page", () => {
  assert.deepEqual(pageStarts([195, 120, 150], 370, 16, 1), [0, 2], "the live Tasks column at 1160 px: two cards fit on the first page, where sizing every page by its 195 px tallest card showed one");
  assert.deepEqual(pageStarts([120, 120, 120, 200, 90, 90], 500, 16, 1), [0, 3], "three cards fill the first page, and the tall fourth starts the next with the two short ones after it");
  assert.deepEqual(pageStarts([100, 100, 100, 100], 448, 16, 1), [0], "four 100 px cards and three gaps are exactly 448 px");
  assert.deepEqual(pageStarts([100, 100, 100, 100], 447, 16, 1), [0, 3]);
  assert.deepEqual(pageStarts([600, 100], 500, 16, 1), [0, 1], "a card taller than the list still gets a page of its own");
  assert.deepEqual(pageStarts([], 500, 16, 1), [0], "an empty list is one empty page");
});

test("in a grid, rows of cards fill the page, each row as tall as its tallest card", () => {
  assert.deepEqual(pageStarts([100, 150, 100, 100, 200, 50], 300, 16, 2), [0, 4], "rows of 150 and 100 px fit in 300 px; the 200 px row starts the next page");
});

test("a ranked card is measured apart at the top of its list, where it renders differently", () => {
  assert.equal(fitKey("task-a", 0), "task-a#first");
  assert.equal(fitKey("task-a", 1), "task-a");
  assert.equal(fitKey("task-a", 7), "task-a");
});

test("the page stays inside the list as cards come and go", () => {
  assert.equal(clampPage(3, 4), 3);
  assert.equal(clampPage(4, 4), 3);
  assert.equal(clampPage(2, 1), 0);
  assert.equal(clampPage(-1, 4), 0);
});

test("a card moved past its page's edge is shown on the page it moved to", () => {
  const starts = [0, 3, 5];
  assert.equal(pageOf(2, starts), 0, "the third card is on the first page");
  assert.equal(pageOf(3, starts), 1, "one place further down carries the view to the second page");
  assert.equal(pageOf(4, starts), 1);
  assert.equal(pageOf(7, starts), 2);
});

test("the pager says which cards show, and a list that fits needs none", () => {
  assert.equal(pageLabel(0, 5, 18), "1–5 of 18");
  assert.equal(pageLabel(15, 18, 18), "16–18 of 18");
  assert.equal(pageLabel(0, 5, 5), "");
  assert.equal(pageLabel(0, 0, 0), "");
});

test("a sideways swipe turns the page and a vertical one scrolls", () => {
  assert.equal(swipeStep(-80, 10), 1, "swiping left shows the next cards");
  assert.equal(swipeStep(80, -12), -1, "swiping right shows the earlier cards");
  assert.equal(swipeStep(-30, 0), 0, "too short to be a swipe");
  assert.equal(swipeStep(-80, 90), 0, "mostly vertical");
});

test("side by side, a list fits the canvas below it; stacked, a list fits one screen below its column heading", () => {
  assert.equal(availableHeight({ view: 900, top: 400, heading: 120, stacked: false, reserve: 90 }), 410, "900 px of canvas, the list starts 400 px down, 90 px kept for the pager and padding");
  assert.equal(availableHeight({ view: 900, top: 2400, heading: 120, stacked: true, reserve: 90 }), 690, "a stacked column far down the page still gets a screenful");
  assert.equal(availableHeight({ view: 300, top: 400, heading: 120, stacked: false, reserve: 90 }), 0, "never negative");
});
