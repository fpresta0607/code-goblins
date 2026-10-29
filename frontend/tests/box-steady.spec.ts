import { expect, test, type Page } from "./site";
import { box, open, openGoblin } from "./session-board";

// The Overlord, 2026-10-28, from his phone: "The chat input bar when it's
// reconnecting, it's a small little text, like when it's reconnecting, it
// just shifts everything off." A dropped connection moves nothing: the box
// and the conversation keep their place and size while it reconnects and
// after. Meanwhile the box queues what he sends with no words for it (2026-10-28: "this text not needed on working goblins
// resumes are assumed").

// places reads where the box and the conversation are drawn.
const places = (page: Page) => page.evaluate(() => [".composer-dock", ".conversation", ".composer-input"].map((selector) => {
  const shape = document.querySelector(selector)?.getBoundingClientRect();
  return shape ? [Math.round(shape.x), Math.round(shape.y), Math.round(shape.width), Math.round(shape.height)] : null;
}));

for (const width of [393, 1280]) {
  test(`at ${width} px a dropped connection moves neither the box nor the conversation, while it reconnects and after`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 852 });
    const relay = await open(page);
    await openGoblin(page);
    await expect.poll(() => relay.views).toBeGreaterThan(0);
    await expect(box(page)).toBeVisible();
    const before = await places(page);
    expect(before.every(Boolean), "the box and the conversation are drawn").toBe(true);

    // Act: the connection drops and stays down.
    relay.refuse();
    relay.drop();

    // Assert: the box queues, and nothing moved.
    await expect(box(page)).toHaveAttribute("data-queues", "");
    expect(await places(page)).toEqual(before);
    expect(await box(page).locator(".composer-banner").count()).toBe(0);

    // Act: it comes back.
    const views = relay.views;
    relay.accept();
    await expect.poll(() => relay.views).toBeGreaterThan(views);
    await expect(box(page)).not.toHaveAttribute("data-queues", "");

    // Assert
    expect(await places(page)).toEqual(before);
  });
}
