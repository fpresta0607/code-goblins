import { expect, type Locator, type Page } from "./site";
import { at, GOBLIN, NAME, open, openGoblin, snapshot } from "./session-board";

const conversation = "fixture-conversation";
const reply = (seq: number, text: string) => ({ id: "m" + seq, seq, at, kind: "reply", blocks: [{ kind: "text", text }] });
const messages = Array.from({ length: 1000 }, (_, seq) => reply(seq, "Answer " + seq + "\n\nMore to read."));
const action = { id: "q1", kind: "message", task_id: GOBLIN.id, generation: GOBLIN.generation, status: "queued", text: "Waiting for delivery", created_at: at, updated_at: at };

async function settled(talk: Locator) {
  await talk.evaluate(async () => { for (let frame = 0; frame < 4; frame++) await new Promise(requestAnimationFrame); });
}

export async function delayedScroll(page: Page, owner: "CFO" | "goblin", movement: "keyboard" | "wheel", update: "stream" | "board") {
  const queued = { ...action, task_id: owner === "CFO" ? "" : GOBLIN.id };
  await open(page, snapshot({ actions: [queued] }), "", { messages, conversation, more: false });
  if (owner === "CFO") await page.keyboard.press("Control+Alt+1"); else await openGoblin(page);
  const talk = page.getByRole("log", { name: "Messages of " + (owner === "CFO" ? "CFO" : NAME) });
  await expect(talk.locator('[data-message="m999"]')).toBeVisible();
  await expect(talk.locator('[data-queued="q1"]')).toBeVisible();
  await settled(talk);
  if (movement === "keyboard") {
    await page.addStyleTag({ content: ".conversation-items > [data-message] { height: 160px; min-height: 160px; box-sizing: border-box; } .conversation-items { row-gap: 18px; } .conversation-room { margin-top: -18px; }" });
    await settled(talk);
    for (let step = 0; step < 2; step++) {
      await talk.evaluate((element) => element.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", shiftKey: true, bubbles: true })));
      await settled(talk);
    }
    await page.getByRole("button", { name: "Jump to latest" }).click();
    await settled(talk);
  }
  const before = await talk.evaluate((element, { movement, update, queued, board }) => {
    const guard = (event: Event) => { if (event.target === element) event.stopImmediatePropagation(); };
    window.addEventListener("scroll", guard, true);
    Object.assign(window, { releaseScroll: () => window.removeEventListener("scroll", guard, true) });
    const end = element.scrollTop;
    if (movement === "keyboard") element.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", shiftKey: true, bubbles: true }));
    else element.scrollTop -= 80;
    const top = element.getBoundingClientRect().top;
    const row = [...element.querySelectorAll<HTMLElement>("[data-message]")].find((item) => item.getBoundingClientRect().bottom > top)!;
    Object.assign(window, { raceRow: row });
    const place = { end, top: element.scrollTop, height: element.scrollHeight, id: row.dataset.message!, text: row.textContent, offset: row.getBoundingClientRect().top - top };
    if (update === "stream") window.dispatchEvent(new CustomEvent("fixture-messages", { detail: { type: "messages", data: { conversation: "fixture-conversation", messages: [
      { id: place.id, seq: Number(place.id.slice(1)), at: "2026-10-22T09:00:00Z", kind: "reply", blocks: [{ kind: "text", text: "Changed during scroll" }] },
      { id: "m1000", seq: 1000, at: "2026-10-22T09:00:00Z", kind: "reply", blocks: [{ kind: "text", text: "Newest during scroll" }] },
    ] } } }));
    else window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: { ...board, revision: 2, actions: [{ ...queued, status: "succeeded" }] } }));
    return place;
  }, { movement, update, queued, board: snapshot() });
  expect(before.top).toBeLessThan(before.end - 24);
  await settled(talk);
  const after = await talk.evaluate((element, id) => {
    const row = element.querySelector<HTMLElement>(`[data-message="${id}"]`);
    return { top: element.scrollTop, height: element.scrollHeight, same: row === (window as unknown as { raceRow: Element }).raceRow,
      text: row?.textContent, offset: row && row.getBoundingClientRect().top - element.getBoundingClientRect().top };
  }, before.id);
  expect(after).toEqual({ top: before.top, height: before.height, same: true, text: before.text, offset: before.offset });
  await expect(talk.locator('[data-queued="q1"]')).toHaveCount(1);
  const jump = page.getByRole("button", { name: "Jump to latest" });
  await expect(jump).toBeVisible();
  await jump.evaluate((button: HTMLButtonElement) => button.click());
  await talk.evaluate((element) => {
    (window as unknown as { releaseScroll: () => void }).releaseScroll();
    element.dispatchEvent(new Event("scroll"));
  });
  if (update === "stream") await expect(talk.locator('[data-message="m1000"]')).toHaveText(/Newest during scroll/);
  else await expect(talk.locator('[data-queued="q1"]')).toHaveCount(0);
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
  await settled(talk);
  await expect.poll(() => talk.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight)).toBeLessThanOrEqual(1);
}
