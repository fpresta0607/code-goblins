import { devices, expect, servePages, test, type Page } from "./site";
import { field, open, openGoblin, snapshot } from "./session-board";

// The Overlord, 2026-10-28, from his iPhone: "remembered iOS keyboard
// polish". On WebKit as an iPhone, at the private board's address (WebKit
// refuses the suite's own port): the box's text is 16 px or more, so Safari
// never zooms the page as it takes the keyboard; the page asks for the
// safe-area insets; with the keyboard up the box sits on it, the header stays
// put and the newest message stays in view; and with it down again no blank
// band is left under the box. The supervisor is played by
// tests/session-board.ts.
const PRIVATE = "https://pc.example.ts.net:4311";
const UP = { width: 393, height: 420 };
const DOWN = { width: 393, height: 852 };

test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE, viewport: DOWN });

const talk = { messages: Array.from({ length: 12 }, (_, seq) => ({ id: "m" + seq, seq, at: "2026-10-22T09:00:00Z", kind: seq % 2 ? "reply" : "prompt", source: seq % 2 ? undefined : "overlord", text: seq % 2 ? undefined : "Question " + seq, blocks: seq % 2 ? [{ kind: "text", text: "Answer " + seq + " runs over a line or two of plain prose." }] : undefined })), more: false };

// layout reads where the header, the box and the newest message are.
const layout = (page: Page) => page.evaluate(() => {
  const place = (selector: string) => document.querySelector(selector)!.getBoundingClientRect();
  const rows = document.querySelectorAll(".conversation [data-message]");
  return { header: place(".phone-header").top, box: place(".composer-input"), newest: rows[rows.length - 1].getBoundingClientRect(), log: place(".conversation"), height: innerHeight, scale: visualViewport?.scale ?? 1, wide: document.documentElement.scrollWidth > innerWidth };
});

test("on an iPhone the box takes the keyboard with no zoom, rides on it with the newest message in view, and leaves no band when it goes", async ({ page }) => {
  // Arrange
  await servePages(page.context(), PRIVATE);
  await open(page, snapshot({}, { phase: "settled" }), "", talk);
  await openGoblin(page);
  const before = await layout(page);

  // Assert: the page asks for the safe areas, and the box's text cannot zoom it.
  expect(await page.evaluate(() => document.querySelector<HTMLMetaElement>('meta[name="viewport"]')!.content)).toContain("viewport-fit=cover");
  expect(await field(page).evaluate((element) => parseFloat(getComputedStyle(element).fontSize))).toBeGreaterThanOrEqual(16);

  // Act: he taps the box, and the keyboard takes the bottom of the screen.
  await field(page).tap();
  await page.setViewportSize(UP);

  // Assert
  await expect.poll(async () => (await layout(page)).box.bottom).toBeLessThanOrEqual(UP.height);
  const up = await layout(page);
  expect(up.scale, "no zoom").toBe(1);
  expect(up.header, "the header stays put").toBe(before.header);
  expect(UP.height - up.box.bottom, "the box sits on the keyboard").toBeLessThanOrEqual(24);
  expect(up.newest.bottom, "the newest message is in view").toBeLessThanOrEqual(Math.min(up.log.bottom, up.box.top) + 1);
  expect(up.newest.bottom).toBeGreaterThan(up.log.top);
  expect(up.wide, "no sideways scroll").toBe(false);

  // Act: the keyboard goes.
  await page.setViewportSize(DOWN);

  // Assert
  await expect.poll(async () => DOWN.height - (await layout(page)).box.bottom, "no blank band under the box").toBeLessThanOrEqual(24);
});
