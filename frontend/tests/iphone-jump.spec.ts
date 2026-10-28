import { devices, expect, servePages, test } from "./site";
import { open, openGoblin, snapshot } from "./session-board";

// The Overlord, 2026-10-28, from his iPhone: "Jump to latest on mobile
// doesn't go away". On WebKit as an iPhone, at the private board's address
// (WebKit refuses the suite's own port), the button shows while he reads up
// and leaves once the newest message is in view. The supervisor is played by
// tests/session-board.ts.
const PRIVATE = "https://pc.example.ts.net:4311";

test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE });

const at = (seq: number) => new Date(Date.parse("2026-10-22T09:00:00Z") + seq * 60_000).toISOString();
const LONG = { messages: Array.from({ length: 60 }, (_, seq) => seq % 2
  ? { id: "r" + seq, seq, at: at(seq), kind: "reply", blocks: [{ kind: "text", text: "Answer " + seq + " runs over a line or two of plain prose for the phone." }] }
  : { id: "p" + seq, seq, at: at(seq), kind: "prompt", source: "overlord", text: "Question " + seq }), more: false };

test("on an iPhone Jump to latest leaves once the newest message is in view", async ({ page }) => {
  // Arrange
  await servePages(page.context(), PRIVATE);
  await open(page, snapshot({}, { phase: "settled" }), "", LONG);
  await openGoblin(page);
  const jumpButton = page.getByRole("button", { name: "Jump to latest", exact: true });
  const log = page.getByRole("log");
  await expect(log).toContainText("Answer 59");

  // Act: he reads up.
  await log.evaluate((element) => { element.scrollTop = 0; });

  // Assert
  await expect(jumpButton).toBeVisible();

  // Act: he scrolls back until the newest message is wholly in view, short
  // of the list's own end under its padding.
  await log.evaluate((element) => { element.scrollTop = element.scrollHeight - element.clientHeight - 36; });

  // Assert
  await expect(log.locator('[data-message="r59"]')).toBeInViewport();
  await expect(jumpButton).toHaveCount(0);
});
