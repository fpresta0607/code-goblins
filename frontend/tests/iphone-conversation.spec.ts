import { devices, expect, servePages, test } from "./site";
import { at, NAME, open, openGoblin, snapshot } from "./session-board";

const PRIVATE = "https://pc.example.ts.net:4311";
const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const MESSAGES = Array.from({ length: 1000 }, (_, seq) => reply(seq, "Answer " + seq + "\n\nA few more lines to read."));

test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE });

test("WebKit keeps streamed history still and a touch Jump cancels the glide to reach the coalesced latest reply", async ({ page }, info) => {
  // Arrange
  await servePages(page.context(), PRIVATE);
  await open(page, snapshot(), "", { messages: MESSAGES, conversation: "fixture-conversation", more: false });
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk).toContainText("Answer 999");
  await talk.evaluate((element) => { element.scrollTop -= 500; });
  let prior = "";
  await expect.poll(async () => {
    const next = await talk.evaluate((element) => element.scrollTop + ":" + element.scrollHeight);
    const isQuiet = next === prior;
    prior = next;
    return isQuiet;
  }).toBe(true);
  const before = await talk.evaluate((element) => {
    const top = element.getBoundingClientRect().top;
    const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((row) => row.getBoundingClientRect().bottom > top)!;
    Object.assign(window, { keptRow: row });
    return { id: row.dataset.message!, text: row.textContent, offset: row.getBoundingClientRect().top - top, height: element.scrollHeight };
  });

  // Act
  for (let update = 1; update <= 12; update++) {
    await page.evaluate(async (data) => {
      window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: data } } }));
      await new Promise(requestAnimationFrame);
    }, [reply(Number(before.id.slice(1)), "Changed history " + update), reply(1000, "Newest reply " + update)]);
  }

  // Assert: receiving the stream replaces neither his row nor its words.
  expect(await talk.evaluate((element, before) => {
    const row = element.querySelector(`[data-message="${before.id}"]`)!;
    return { isSame: row === (window as unknown as { keptRow: Element }).keptRow, text: row.textContent,
      offset: row.getBoundingClientRect().top - element.getBoundingClientRect().top, height: element.scrollHeight };
  }, before)).toEqual({ isSame: true, text: before.text, offset: before.offset, height: before.height });

  // A momentum-stopping touch can have no click on a phone (#1771).
  await page.evaluate(() => window.addEventListener("click", (event) => { event.stopImmediatePropagation(); event.preventDefault(); }, true));
  await talk.evaluate((element) => { element.scrollTo({ top: element.scrollTop - 500, behavior: "smooth" }); });
  await page.getByRole("button", { name: "Jump to latest" }).tap();
  await expect(talk.locator('[data-message="m1000"]')).toHaveCount(1);
  await expect(talk.locator('[data-message="m1000"]')).toHaveText(/Newest reply 12/);
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
  await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
  expect(await talk.locator("[data-message]").count()).toBeLessThan(60);
  const picture = info.outputPath("webkit-latest.png");
  await page.screenshot({ path: picture });
  await info.attach("webkit-latest", { path: picture, contentType: "image/png" });
});
