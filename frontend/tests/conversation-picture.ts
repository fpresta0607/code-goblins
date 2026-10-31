import { expect, type Page } from "./site";
import { at, NAME, open, openGoblin, snapshot } from "./session-board";

const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "base64");

export async function heldPictureAfterReset(page: Page, isTouch: boolean) {
  const picture = { id: "same-picture", media_type: "image/png", bytes: PNG.length };
  const rows = Array.from({ length: 1000 }, (_, seq) => ({ id: "m" + seq, seq, at, kind: "prompt", source: "overlord", text: "Question " + seq, pictures: seq === 0 ? [picture] : [] }));
  await open(page, snapshot(), "", { conversation: "old", messages: rows, more: false });
  const requests: (string | null)[] = [];
  await page.route((url) => url.pathname === "/api/messages" && url.searchParams.has("picture"), (route) => {
    const identity = new URL(route.request().url()).searchParams.get("conversation");
    requests.push(identity);
    return identity === "new" ? route.fulfill({ body: PNG, contentType: "image/png" }) : route.fulfill({ status: 409, body: "" });
  });
  await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + NAME });
  await expect(talk.locator('[data-message="m999"]')).toBeVisible();
  await expect(talk.locator('[data-message="m0"]')).toHaveCount(0);
  expect(requests).toEqual([]);
  await talk.evaluate((element) => { element.scrollTop -= 500; });
  await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
  await page.evaluate((data) => window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "page", data } })), { conversation: "new", messages: rows, more: false });
  await talk.evaluate((element) => { element.scrollTop = 0; });
  const row = talk.locator('[data-message="m0"]');
  await expect(row).toBeVisible();
  await expect.poll(() => requests.length).toBeGreaterThan(0);
  expect(requests).toEqual(["old"]);
  const thumbnail = row.getByRole("button", { name: "Open picture 1" });
  if (isTouch) await thumbnail.tap(); else await thumbnail.click();
  const viewer = page.getByRole("dialog", { name: "Pictures" });
  await expect(viewer).toBeVisible();
  await expect.poll(() => requests.length).toBeGreaterThan(1);
  expect(requests.every((identity) => identity === "old")).toBe(true);
  await expect(viewer).toContainText("This image is no longer available.");
  if (isTouch) await viewer.getByRole("button", { name: "Close" }).tap(); else await viewer.getByRole("button", { name: "Close" }).click();
  if (isTouch) await page.getByRole("button", { name: "Jump to latest" }).tap(); else await page.getByRole("button", { name: "Jump to latest" }).click();
  await expect(talk.locator('[data-message="m999"]')).toBeVisible();
  await talk.evaluate((element) => { element.scrollTop = 0; });
  await expect(row).toBeVisible();
  const image = row.locator(".message-picture img");
  await expect.poll(() => image.evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth)).toBe(1);
  expect(requests.at(-1)).toBe("new");
  if (isTouch) await thumbnail.tap(); else await thumbnail.click();
  await expect(viewer).toBeVisible();
  await expect.poll(() => viewer.locator("img").evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth)).toBe(1);
  expect(new URL(await viewer.locator("img").getAttribute("src") ?? "", page.url()).searchParams.get("conversation")).toBe("new");
}
