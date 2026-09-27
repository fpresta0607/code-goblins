import { test } from "node:test";
import assert from "node:assert/strict";
import { availableHeight, clampPage, pageLabel, pageOf, pageSizeFor, swipeStep, tallestCard } from "./fit.ts";

test("a page holds as many cards as fit the list's height, by rows of its grid", () => {
  assert.equal(pageSizeFor(500, 100, 16, 1), 4, "four 100 px cards and three gaps fit in 500 px, a fifth does not");
  assert.equal(pageSizeFor(448, 100, 16, 1), 4, "four cards and three gaps are exactly 448 px");
  assert.equal(pageSizeFor(447, 100, 16, 1), 3);
  assert.equal(pageSizeFor(500, 100, 16, 3), 12, "a grid of three columns shows four rows of three");
  assert.equal(pageSizeFor(40, 100, 16, 1), 1, "a list too short for one card still shows one");
  assert.equal(pageSizeFor(500, 0, 16, 1), 1, "no card measured yet shows one, then fits again once measured");
});

test("a page is sized by the tallest card seen at its width, so shorter cards never flip the size back", () => {
  const seen = tallestCard({ width: 300, unit: 0 }, 300, [90, 90, 90, 90, 130]);
  assert.equal(pageSizeFor(600, seen.unit, 16, 1), 4, "a wrapped 130 px card leaves room for four");
  const shorter = tallestCard(seen, 300, [90, 90, 90, 90]);
  assert.equal(pageSizeFor(600, shorter.unit, 16, 1), 4, "the four one-line cards it then shows keep four, not five");
  assert.equal(tallestCard(shorter, 420, [90, 90]).unit, 90, "a new width measures again");
});

test("the page stays inside the list as cards come and go", () => {
  assert.equal(clampPage(3, 5, 18), 3);
  assert.equal(clampPage(4, 5, 18), 3);
  assert.equal(clampPage(2, 5, 0), 0);
  assert.equal(clampPage(-1, 5, 18), 0);
});

test("a card moved past its page's edge is shown on the page it moved to", () => {
  assert.equal(pageOf(4, 5), 0, "the fifth card is on the first page of five");
  assert.equal(pageOf(5, 5), 1, "one place further down carries the view to the second page");
  assert.equal(pageOf(0, 5), 0);
  assert.equal(pageOf(17, 5), 3);
});

test("the pager says which cards show, and a list that fits needs none", () => {
  assert.equal(pageLabel(0, 5, 18), "1–5 of 18");
  assert.equal(pageLabel(3, 5, 18), "16–18 of 18");
  assert.equal(pageLabel(0, 5, 5), "");
  assert.equal(pageLabel(0, 5, 0), "");
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
