import { expect, holdStream, test, type Page } from "./site";

// The grids the terminal's view asked its terminal for, newest last.
let grids: { cols: number; rows: number }[];

const STORED = "cfo-terminal-font-size";
const stored = (page: Page) => page.evaluate((key) => localStorage.getItem(key), STORED);
const terminal = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });

// The first line says ready; the cursor is on the next line, outside this
// crop, so moving focus cannot masquerade as a change of the rendered text.
async function textPixels(page: Page) {
  const screen = (await page.locator(".xterm-screen").boundingBox())!;
  return page.screenshot({ clip: { x: screen.x, y: screen.y, width: 100, height: 24 } });
}

// Adjacent font sizes can round to the same cell dimensions. The rendered
// text must change, both grid dimensions must move in the right direction,
// and the drawn grid must still fill the panel.
async function sized(page: Page, size: number, was: number, act: () => Promise<unknown>) {
  const pixels = await textPixels(page);
  const before = grids.at(-1)!;
  await act();
  await expect.poll(() => stored(page)).toBe(String(size));
  await expect.poll(async () => !(await textPixels(page)).equals(pixels), { message: size + " px text after " + was }).toBe(true);
  await expect.poll(() => page.locator(".terminal-view").evaluate((view, grid) => {
    const panel = view.getBoundingClientRect();
    const screen = view.querySelector(".xterm-screen")!.getBoundingClientRect();
    const cell = { width: screen.width / grid.cols, height: screen.height / grid.rows };
    const side = (panel.width - screen.width) / 2;
    return {
      hasFullColumns: side >= 10 && side < 10 + cell.width / 2,
      hasFullRows: panel.height - 2 * side - screen.height >= -0.1 && panel.height - 2 * side - screen.height < cell.height,
      isCentered: Math.abs(screen.left - panel.left - side) < 0.1,
      isInputAtBottom: Math.abs(panel.bottom - screen.bottom - side) < 0.1,
    };
  }, grids.at(-1)!)).toEqual({ hasFullColumns: true, hasFullRows: true, isCentered: true, isInputAtBottom: true });
  const after = grids.at(-1)!;
  if (size > was) {
    expect(after.cols).toBeLessThanOrEqual(before.cols);
    expect(after.rows).toBeLessThanOrEqual(before.rows);
  } else {
    expect(after.cols).toBeGreaterThanOrEqual(before.cols);
    expect(after.rows).toBeGreaterThanOrEqual(before.rows);
  }
}

async function openTerminal(page: Page) {
  grids = [];
  await page.locator(".task-board .task-card").filter({ hasText: "size-proof" }).click();
  await page.locator(".context-pane .panel-pill").getByRole("button", { name: "Terminal", exact: true }).click();
  await expect(terminal(page)).toBeVisible();
  await expect(page.getByText("Connecting to the terminal", { exact: true })).toHaveCount(0);
  await page.evaluate(() => document.fonts.ready);
  await expect.poll(() => grids.length).toBeGreaterThan(0);
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
  await page.addInitScript(() => {
    if (!localStorage.getItem("cfo-pane-width")) localStorage.setItem("cfo-pane-width", "1000");
    localStorage.setItem("cfo-terminal-maximized", "false");
  });
  await holdStream(page, { healthy: true, instance: "fixture", cfo_runs: true, cfo_harness: "claude", tasks: [
    { id: "size-proof", title: "size-proof", project: "code-goblins", phase: "working", verified: false, generation: "proof", harness: "codex", backend: "native", since: "2026-10-03T00:00:00Z" },
  ] });
  await page.route("**/api/workspace", (route) => route.fulfill({ json: { repository: "code-goblins", root: "C:/work/code-goblins", harness: "codex", notes: [] } }));
  await page.goto("/tests/fixtures/terminal-text-size.html");
  await openTerminal(page);
});

for (const [where, focus] of [
  ["the terminal", (page: Page) => terminal(page).focus()],
  ["the panel around it", (page: Page) => page.locator(".context-pane").focus()],
  ["a button of the panel", (page: Page) => page.getByRole("button", { name: "Larger terminal text", exact: true }).focus()],
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
      await sized(page, next, size, () => page.keyboard.press(key));
      size = next;
    }
  });
}

test("the wheel with Ctrl held over the terminal sizes the text, and the wheel alone leaves it", async ({ page }) => {
  const box = (await page.locator(".terminal-surface").boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);

  await page.mouse.wheel(0, -100);
  expect(await stored(page)).toBeNull();
  await page.keyboard.down("Control");
  await sized(page, 21, 20, () => page.mouse.wheel(0, -100));
  await sized(page, 19, 21, () => page.mouse.wheel(0, 200));
  await page.keyboard.up("Control");
});

test("a pinch, which arrives as small turns of that wheel, adds up to a step and never reaches the page", async ({ page }) => {
  await sized(page, 21, 20, async () => {
    const taken = await page.locator(".terminal-view").evaluate((view) => [-30, -30, -30, -30].map((deltaY) =>
      !view.dispatchEvent(new WheelEvent("wheel", { deltaY, ctrlKey: true, bubbles: true, cancelable: true }))));
    expect(taken).toEqual([true, true, true, true]);
  });
});

test("the panel's buttons size the text, and it is remembered across a reload", async ({ page }) => {
  await sized(page, 21, 20, () => page.getByRole("button", { name: "Larger terminal text" }).click());
  await sized(page, 22, 21, () => page.getByRole("button", { name: "Larger terminal text" }).click());
  await sized(page, 21, 22, () => page.getByRole("button", { name: "Smaller terminal text" }).click());

  const chosen = grids.at(-1)!;
  grids = [];
  await page.reload();
  await openTerminal(page);
  expect(await stored(page)).toBe("21");
  await expect.poll(() => grids.at(-1)).toEqual(chosen);

  await page.locator(".context-pane").focus();
  await sized(page, 20, 21, () => page.keyboard.press("Control+0"));
});

test("the buttons stop at the smallest and the largest size", async ({ page }) => {
  await page.evaluate((key) => localStorage.setItem(key, "12"), STORED);
  await page.reload();
  await openTerminal(page);
  await expect(page.getByRole("button", { name: "Smaller terminal text" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Larger terminal text" })).toBeEnabled();

  await page.evaluate((key) => localStorage.setItem(key, "28"), STORED);
  await page.reload();
  await openTerminal(page);
  await expect(page.getByRole("button", { name: "Larger terminal text" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Smaller terminal text" })).toBeEnabled();
});

test("Smaller and Larger belong to the fitted top row above a terminal", async ({ page }) => {
  const row = page.locator(".context-pane .panel-top");
  for (const name of ["Smaller terminal text", "Larger terminal text"]) {
    await expect(row.getByRole("button", { name, exact: true })).toBeVisible();
    await expect(page.locator(".panel-header").getByRole("button", { name, exact: true })).toHaveCount(0);
  }
});

test("the text controls work from More and its keyboard when the panel is narrow", async ({ page }) => {
  await page.evaluate(() => localStorage.setItem("cfo-pane-width", "360"));
  await page.reload();
  await openTerminal(page);
  const row = page.locator(".context-pane .panel-top");
  const more = row.getByRole("button", { name: "More", exact: true });
  await expect(row.getByRole("button", { name: "Larger terminal text", exact: true })).toHaveCount(0);
  await sized(page, 21, 20, async () => {
    await more.click();
    await row.getByRole("menuitem", { name: "Larger text", exact: true }).click();
  });
  await expect(more).toBeFocused();
  await sized(page, 22, 21, () => page.keyboard.press("Control+Equal"));
  await sized(page, 20, 22, () => page.keyboard.press("Control+0"));
  await sized(page, 19, 20, async () => {
    await more.click();
    await row.getByRole("menuitem", { name: "Smaller text", exact: true }).click();
  });
});

for (const size of [12, 28]) {
  test("More keeps the exact font bound at " + size + " px and skips its disabled item", async ({ page }) => {
    await page.evaluate(({ key, value }) => {
      localStorage.setItem(key, String(value));
      localStorage.setItem("cfo-pane-width", "360");
    }, { key: STORED, value: size });
    await page.reload();
    await openTerminal(page);
    const row = page.locator(".context-pane .panel-top");
    await row.getByRole("button", { name: "More", exact: true }).click();
    const disabled = row.getByRole("menuitem", { name: size === 12 ? "Smaller text" : "Larger text", exact: true });
    const enabled = row.getByRole("menuitem", { name: size === 12 ? "Larger text" : "Smaller text", exact: true });
    await expect(disabled).toBeDisabled();
    await expect(enabled).toBeEnabled();
    await expect(row.getByRole("menuitem")).toHaveText(["Smaller text", "Larger text", "Open in terminal"]);
    await expect(enabled).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(row.getByRole("menuitem", { name: "Open in terminal", exact: true })).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(enabled).toBeFocused();
    await page.keyboard.press("ArrowUp");
    await expect(row.getByRole("menuitem", { name: "Open in terminal", exact: true })).toBeFocused();
    await page.keyboard.press("ArrowUp");
    await expect(enabled).toBeFocused();
    expect(await stored(page)).toBe(String(size));
  });
}
