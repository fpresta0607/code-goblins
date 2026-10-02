import { expect, test, type Page } from "./site";

// The grids the terminal's view asked its terminal for, newest last: a larger
// text size leaves room for fewer columns.
let grids: { cols: number; rows: number }[];

const STORED = "cfo-terminal-font-size";
const readout = (page: Page) => page.getByRole("button", { name: /^Reset the terminal text size/ });
const stored = (page: Page) => page.evaluate((key) => localStorage.getItem(key), STORED);
const terminal = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });

// sized waits for the size to show on the panel, be remembered, and reach the
// terminal as a grid of fewer columns when larger and more when smaller.
async function sized(page: Page, size: number, was: number) {
  const before = grids.at(-1)!.cols;
  await expect(readout(page)).toHaveText(String(size));
  expect(await stored(page)).toBe(String(size));
  if (size > was) await expect.poll(() => grids.at(-1)!.cols, { message: size + " px after " + was }).toBeLessThan(before);
  if (size < was) await expect.poll(() => grids.at(-1)!.cols, { message: size + " px after " + was }).toBeGreaterThan(before);
}

test.beforeEach(async ({ page }) => {
  grids = [];
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
    socket.send(Buffer.from("\x1b[2J\x1b[Hready\r\n"));
    socket.onMessage((data) => {
      if (typeof data !== "string") return;
      const message: { type: string; cols: number; rows: number } = JSON.parse(data);
      if (message.type !== "resize") return;
      grids.push({ cols: message.cols, rows: message.rows });
      // The terminal takes the size, as the relay reports it.
      socket.send(JSON.stringify({ type: "size", cols: message.cols, rows: message.rows }));
    });
  });
  await page.goto("/tests/fixtures/terminal-text-size.html");
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
  await expect.poll(() => grids.length).toBeGreaterThan(0);
  await expect(readout(page)).toHaveText("20");
});

for (const [where, focus] of [
  ["the terminal", (page: Page) => terminal(page).focus()],
  ["the panel around it", (page: Page) => page.getByRole("complementary", { name: "Goblin panel" }).focus()],
  ["a button of the panel", (page: Page) => readout(page).focus()],
] as const) {
  test("with the keyboard on " + where + ", Ctrl with plus, minus and zero size the text", async ({ page }) => {
    await focus(page);
    let size = 20;
    for (const [key, next] of [
      ["Control+Equal", 21], ["Control+Shift+Equal", 22], ["Control+NumpadAdd", 23],
      ["Control+0", 20],
      ["Control+Minus", 19], ["Control+NumpadSubtract", 18],
      ["Control+0", 20],
    ] as const) {
      await page.keyboard.press(key);
      await sized(page, next, size);
      size = next;
    }
  });
}

test("the wheel with Ctrl held over the terminal sizes the text, and the wheel alone leaves it", async ({ page }) => {
  const box = (await page.locator(".terminal-surface").boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);

  await page.mouse.wheel(0, -100);
  await expect(readout(page)).toHaveText("20");
  await page.keyboard.down("Control");
  await page.mouse.wheel(0, -100);
  await sized(page, 21, 20);
  await page.mouse.wheel(0, 200);
  await sized(page, 19, 21);
  await page.keyboard.up("Control");
});

test("a pinch, which arrives as small turns of that wheel, adds up to a step and never reaches the page", async ({ page }) => {
  const taken = await page.locator(".terminal-view").evaluate((view) => [-30, -30, -30, -30].map((deltaY) =>
    !view.dispatchEvent(new WheelEvent("wheel", { deltaY, ctrlKey: true, bubbles: true, cancelable: true }))));

  expect(taken).toEqual([true, true, true, true]);
  await sized(page, 21, 20);
});

test("the panel's buttons size the text, and it is remembered across a reload", async ({ page }) => {
  await page.getByRole("button", { name: "Larger terminal text" }).click();
  await sized(page, 21, 20);
  await page.getByRole("button", { name: "Larger terminal text" }).click();
  await sized(page, 22, 21);
  await page.getByRole("button", { name: "Smaller terminal text" }).click();
  await sized(page, 21, 22);

  const chosen = grids.at(-1)!.cols;
  grids = [];
  await page.reload();
  await expect(readout(page)).toHaveText("21");
  await expect.poll(() => grids.at(-1)?.cols).toBe(chosen);

  await readout(page).click();
  await sized(page, 20, 21);
});

test("the buttons stop at the smallest and the largest size", async ({ page }) => {
  await page.evaluate((key) => localStorage.setItem(key, "12"), STORED);
  await page.reload();
  await expect(page.getByRole("button", { name: "Smaller terminal text" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Larger terminal text" })).toBeEnabled();

  await page.evaluate((key) => localStorage.setItem(key, "28"), STORED);
  await page.reload();
  await expect(page.getByRole("button", { name: "Larger terminal text" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Smaller terminal text" })).toBeEnabled();
});
