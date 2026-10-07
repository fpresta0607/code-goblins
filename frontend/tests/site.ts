import { existsSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { test as base, type BrowserContext, type Page } from "@playwright/test";

export * from "@playwright/test";

// The origin the pages are given: this machine, as the board is really served,
// so it behaves as it does for the Overlord at his PC, and on port 1, which
// Chromium refuses to connect to. A request no test answers therefore fails at
// once and can never reach whatever happens to listen on a port here.
export const ORIGIN = "http://127.0.0.1:1";

// servePages answers a browser context's requests for the board's pages from
// the folder tests/build-site.ts built them into, in place of a web server. No
// request opens a socket, so a connection the machine refuses cannot fail a
// page load. No supervisor runs behind the tests either, so an API request a
// test does not answer itself fails. origin serves the pages under another
// origin too, for a test of the board as another machine sees it. Its browser
// has had the board's quick tour, so no tour covers the board on a first
// open, unless hadTour is false.
export async function servePages(context: BrowserContext, origin = ORIGIN, hadTour = true): Promise<void> {
  const site = process.env.BOARD_TEST_SITE!;
  if (hadTour) await context.addInitScript(() => localStorage.setItem("cfo-tour", "seen"));
  await context.route(origin + "/**", (route) => {
    const { pathname } = new URL(route.request().url());
    const file = path.join(site, pathname === "/" ? "index.html" : decodeURIComponent(pathname));
    if (!pathname.startsWith("/api/") && existsSync(file)) return route.fulfill({ path: file });
    return route.fulfill({ status: pathname.startsWith("/api/") ? 502 : 404, body: "" });
  });
}

// holdStream gives a page the supervisor's stream as the supervisor serves
// it: one snapshot, on a connection that stays open. A stream answered from a
// route ends as soon as it is read, so the board reconnects for as long as the
// test runs, and its lost-connection banner comes and goes above the board,
// which moves every card under a pointer at rest. A test that points at parts
// one after another, or holds a drag, uses this instead.
export async function holdStream(page: Page, snapshot: unknown): Promise<void> {
  await page.addInitScript((data) => {
    class HeldStream extends EventTarget {
      onerror: (() => void) | null = null;
      constructor() {
        super();
        setTimeout(() => this.dispatchEvent(new MessageEvent("snapshot", { data })));
      }
      close() {}
    }
    Object.defineProperty(window, "EventSource", { value: HeldStream });
  }, JSON.stringify(snapshot));
}

// Outside CI a run shares this machine with the fleet. Windows takes about
// half a minute to release a test's browser process after the test ends, so a
// run holds what its last half minute of tests used: for a fast spec,
// gigabytes. A test therefore starts only once this much memory is free.
const ROOM = 5 * 2 ** 30;

// What a wait for room reads and how long it waits; a test of the wait itself
// replaces them.
// openItem opens the Command Center on the waiting item whose row in its
// list reads text, as the Overlord does: nothing opens by itself.
export async function openItem(page: Page, text: string): Promise<void> {
  const menu = page.locator(".command-center-menu");
  if (!await menu.evaluate((element) => (element as HTMLDetailsElement).open)) await menu.locator("> summary").click();
  await menu.locator(".inbox-list li").filter({ hasText: text }).getByRole("button", { name: /^Answer / }).click();
}

// A card that finishes shows its check for three quarters of a second before
// the Command Center moves on, which a loaded machine can let pass between two
// looks, so a board's done cards are recorded as they pass, each as its
// heading and line. Start recording before the act that finishes the card.
export const recordDoneCards = (page: Page) => page.evaluate(() => {
  const seen: string[] = [];
  Object.assign(window, { doneCards: seen });
  new MutationObserver(() => {
    const text = document.querySelector("dialog.question-modal .done-card")?.textContent;
    if (text && !seen.includes(text)) seen.push(text);
  }).observe(document.body, { subtree: true, childList: true, characterData: true });
});
export const doneCards = (page: Page) => page.evaluate(() => (window as unknown as { doneCards: string[] }).doneCards);

export interface Room { free: () => number; limit: number; pause: number; ci: boolean }
const MACHINE: Room = { free: os.freemem, limit: 60_000, pause: 500, ci: !!process.env.CI };

// roomForATest resolves once free memory reaches ROOM and fails when it has
// not within the limit. CI runs nothing beside the tests, so there it never
// waits.
export async function roomForATest({ free, limit, pause, ci }: Room = MACHINE): Promise<void> {
  if (ci) return;
  const deadline = Date.now() + limit;
  while (free() < ROOM) {
    if (Date.now() >= deadline) throw new Error(`Less than 5 GB of memory was free for ${limit / 1000} s, so this test did not start. Free some memory, or run one spec at a time.`);
    await new Promise((resolve) => setTimeout(resolve, pause));
  }
}

// Every test waits for room before it starts. A test's own context has its
// pages served, with hadTour passed to servePages; a test that opens another
// context serves that one itself.
export const test = base.extend<{ room: Room; paced: void; hadTour: boolean }>({
  room: [MACHINE, { option: true }],
  hadTour: [true, { option: true }],
  paced: [async ({ room }, use) => {
    await roomForATest(room);
    await use();
  }, { auto: true, timeout: MACHINE.limit + 10_000 }],
  context: async ({ context, hadTour }, use) => {
    await servePages(context, ORIGIN, hadTour);
    await use(context);
  },
});
