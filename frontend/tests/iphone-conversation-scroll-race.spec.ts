import { devices, expect, servePages, test } from "./site";
import { delayedScroll, leaveEndWhileStreaming, readUpWhileStreaming, startReplyAtEnd } from "./conversation-scroll-race";
import { at, NAME } from "./session-board";

const PRIVATE = "https://pc.example.ts.net:4311";
test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE });

for (const update of ["stream", "board"] as const) {
  test(`WebKit keeps its place before scroll delivery during a ${update} update`, async ({ page }) => {
    await servePages(page.context(), PRIVATE);
    await delayedScroll(page, "goblin", "wheel", update);
    await page.evaluate((at) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: {
      type: "messages", data: { conversation: "fixture-conversation", messages: [
        { id: "m1001", seq: 1001, at, kind: "reply", blocks: [{ kind: "text", text: "Following after Jump" }] },
      ] },
    } })), at);
    const talk = page.getByRole("log", { name: "Messages of " + NAME });
    await expect(talk.locator('[data-message="m1001"]')).toHaveCount(1);
    await expect(talk.locator('[data-message="m1001"]')).toHaveText(/Following after Jump/);
    await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
    await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);

    await talk.evaluate(async (element) => {
      element.scrollTop -= 80;
      for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame);
    });
    await page.getByRole("button", { name: "Jump to latest" }).evaluate((button: HTMLButtonElement) => button.click());
    await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
    const before = await talk.evaluate((element, at) => {
      element.scrollTop -= 80;
      const top = element.scrollTop;
      window.dispatchEvent(new CustomEvent("fixture-messages", { detail: {
        type: "messages", data: { conversation: "fixture-conversation", messages: [
          { id: "m1001", seq: 1001, at, kind: "reply", blocks: [{ kind: "text", text: "Changed after another Jump" }] },
          { id: "m1002", seq: 1002, at, kind: "reply", blocks: [{ kind: "text", text: "Waiting after another Jump" }] },
        ] },
      } }));
      return top;
    }, at);
    await talk.evaluate(async () => { for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame); });
    expect(await talk.evaluate((element) => element.scrollTop)).toBe(before);
    await expect(talk.locator('[data-message="m1001"]')).toHaveText(/Following after Jump/);
    await expect(talk.locator('[data-message="m1002"]')).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
  });
}

test("WebKit moves nothing in view while he flicks up through a streaming reply", async ({ page }) => {
  test.slow();
  await servePages(page.context(), PRIVATE);
  await readUpWhileStreaming(page, "flick");
});

test("WebKit leaves the end a few pixels up before the next streamed line", async ({ page }) => {
  test.slow();
  await servePages(page.context(), PRIVATE);
  await leaveEndWhileStreaming(page, (talk) => talk.evaluate((element) => { element.scrollTop -= 8; }));
});

test("WebKit keeps following the end when a reply's first line brings its usage", async ({ page }) => {
  await servePages(page.context(), PRIVATE);
  await startReplyAtEnd(page);
});
