import { expect, test, type Locator, type Page } from "./site";

const LANTERN = "rgb(242, 180, 71)";

// Headless Chromium reports notifications as denied, so this browser starts as
// one the board has never asked, which is when it offers them.
async function open(page: Page) {
  await page.addInitScript(() => Object.defineProperty(Notification, "permission", { get: () => "default" }));
  await page.goto("/tests/fixtures/goblin-alerts.html");
  await expect(page.getByRole("group", { name: "Alerts" }).or(page.locator(".toasts"))).toBeVisible();
  await expect(page.locator(".toasts .dialogue")).toHaveCount(4);
}
// The fixture's two bars: one over a board column while a question waits on
// the Overlord, and one at rest.
const waiting = (page: Page) => page.locator(".task-board .cfo-rest");
const resting = (page: Page) => page.locator("main > .cfo-pin .cfo-rest");
const command = (page: Page) => waiting(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" });
const filled = (button: Locator) => button.evaluate((element) => getComputedStyle(element, "::before").backgroundColor);

test("the CFO's bar is the same plain card at rest and while something waits: only then it carries Open Command Center, which glows, counts what waits and says none of it", async ({ page }) => {
  await open(page);
  const needs = waiting(page), rest = resting(page);
  // No lantern box and none of the alert's words: the line says only how many
  // goblins the CFO supervises, and the button how many things wait.
  await expect(page.locator(".cfo-pin .dialogue")).toHaveCount(0);
  await expect(needs.locator("p")).toHaveText("The CFO supervises 3 goblins.");
  await expect(page.locator(".task-board .cfo-pin")).not.toContainText("Which layout should I keep?");
  await expect(command(page)).toHaveText("Open Command Center1");
  await expect(command(page).locator(".cfo-command-count")).toHaveText("1");
  await expect(command(page)).toHaveCSS("animation-name", "cfo-command-glow");
  expect(await command(page).evaluate((button) => getComputedStyle(button).boxShadow)).not.toBe("none");
  await needs.locator("button.icon-button").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its icon");
  await needs.locator("button.cfo-rest-portrait").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its portrait");
  // At rest: no Open Command Center; only its terminal, on the icon and the
  // portrait.
  await expect(rest).toContainText("All quiet. The CFO supervises 3 goblins.");
  await expect(rest.getByRole("button")).toHaveCount(2);
  await expect(rest.getByRole("button", { name: /Open Command Center/ })).toHaveCount(0);
  // Both are the same card as the column under them.
  const look = (locator: Locator) => locator.evaluate((element) => { const style = getComputedStyle(element); return [style.backgroundColor, style.borderTopColor, style.borderRadius].join(" "); });
  for (const bar of [needs, rest]) expect(await look(bar)).toBe(await look(page.locator(".board-column")));
  await rest.locator("button.icon-button").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its icon");
  await rest.locator("button.cfo-rest-portrait").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its portrait");
});

test("the bar keeps its line and its buttons inside it on a phone, at rest and while something waits", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  for (const bar of [resting(page), waiting(page)]) {
    const inside = await bar.evaluate((bar) => {
      const box = bar.getBoundingClientRect();
      return [...bar.children].every((child) => { const part = child.getBoundingClientRect(); return part.left >= box.left && part.right <= box.right && part.top >= box.top && part.bottom <= box.bottom; });
    });
    expect(inside).toBe(true);
    expect(await bar.locator("p").evaluate((line) => parseFloat(getComputedStyle(line).fontSize))).toBeGreaterThanOrEqual(16);
  }
  await expect(command(page)).toBeVisible();
});

test("Open Command Center opens the first item waiting", async ({ page }) => {
  await open(page);
  await command(page).click();
  await expect(page.locator("dialog.question-modal")).toBeVisible();
  await expect(page.locator("dialog.question-modal")).toContainText("Pick the waveform");
});

test("each alert says its news once, in its speaker's box: what needs him opens the Command Center, a goblin's news opens the goblin", async ({ page }) => {
  await open(page);
  const cases: [string, string, boolean][] = [
    ["cg-board-kill asks: Which layout should I keep?", "Open Command Center", true],
    ["cg-board-kill finished: code-goblins #204 is ready.", "Open", false],
    ["pd-billing-admin failed: its checks failed", "Open", false],
  ];
  for (const [text, action, isFilled] of cases) {
    const alert = page.locator(".toasts .dialogue").filter({ hasText: text });
    await expect(alert.locator(".dialogue-text")).toHaveText(text);
    await expect(alert.locator(".dialogue-actions")).toHaveText(action);
    expect(await filled(alert.getByRole("button", { name: action, exact: true })), text).toBe(isFilled ? LANTERN : "rgba(0, 0, 0, 0)");
  }
  await expect(page.getByRole("button", { name: /terminal/i }).filter({ hasText: /Open terminal/ })).toHaveCount(0);
  await page.locator(".toasts .dialogue").filter({ hasText: "finished" }).getByRole("button", { name: "Open", exact: true }).click();
  await expect(page.locator("output")).toHaveText("opened cg-board-kill");
  const ask = page.locator(".toasts .dialogue").filter({ hasText: "Windows notification" });
  expect(await filled(ask.getByRole("button", { name: "Turn on" }))).toBe(LANTERN);
  expect(await filled(ask.getByRole("button", { name: "Not now" }))).toBe("rgba(0, 0, 0, 0)");
});

test("boxes are stepped pixel frames with plain text of 16 px or more, and step up once as they arrive", async ({ page }) => {
  await open(page);
  const look = await page.locator(".dialogue").first().evaluate((element) => {
    const frame = element.querySelector(".dialogue-box")!;
    return { radius: getComputedStyle(frame).borderRadius, clip: getComputedStyle(frame, "::before").clipPath.startsWith("polygon"), animation: getComputedStyle(element).animationName, timing: getComputedStyle(element).animationTimingFunction };
  });
  expect(look).toEqual({ radius: "0px", clip: true, animation: "dialogue-in", timing: "steps(3)" });
  const sizes = await page.locator(".dialogue-text p").evaluateAll((texts) => texts.map((text) => parseFloat(getComputedStyle(text).fontSize)));
  expect(Math.min(...sizes)).toBeGreaterThanOrEqual(16);
});

test("the stepped frames clip no focus ring or tooltip, and a focused Open Command Center shows its outline, in an alert and on the bar", async ({ page }) => {
  await open(page);
  const clipped = await page.locator(".dialogue").evaluateAll((boxes) => boxes.flatMap((dialogue) =>
    [...dialogue.querySelectorAll("button, a[href], input, select, textarea, [tabindex], [data-tip]")].flatMap((element) => {
      const chain: Element[] = [];
      for (let node: Element | null = element; node && node !== dialogue.parentElement; node = node.parentElement) chain.push(node);
      return chain.filter((node) => getComputedStyle(node).clipPath !== "none").map((node) => node.className);
    })));
  expect(clipped).toEqual([]);
  // The bar's button glows with a shadow, which is not its focus ring.
  for (const button of [page.locator(".toasts").getByRole("button", { name: "Open Command Center" }), command(page)]) {
    await button.focus();
    await expect(button).toHaveCSS("outline-style", "solid");
    await expect(button).toHaveCSS("outline-width", "3px");
  }
});

// Whether the tip showing is drawn over everything else where it lies: the
// page's top element at its middle and just inside the middle of each edge,
// clear of its rounded corners, is the tip itself.
async function tipOnTop(page: Page) {
  await page.addStyleTag({ content: ".tip { pointer-events: auto; }" });
  return page.getByRole("tooltip").evaluate((tip) => {
    const box = tip.getBoundingClientRect();
    const across = box.left + box.width / 2, down = box.top + box.height / 2;
    const spots = [[across, down], [across, box.top + 3], [across, box.bottom - 3], [box.left + 3, down], [box.right - 3, down]];
    return spots.every(([x, y]) => document.elementFromPoint(x, y) === tip);
  });
}

test("the bar's portrait tooltip shows over the board column under it", async ({ page }) => {
  await open(page);
  const portrait = waiting(page).locator("button.cfo-rest-portrait");
  await portrait.hover();
  await expect(page.getByRole("tooltip")).toHaveText((await portrait.getAttribute("data-tip"))!);
  expect(await tipOnTop(page)).toBe(true);
});

test("an alert's Dismiss tooltip shows over the next alert", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  // Every alert leaves by itself after eight seconds, so time stands still once
  // the boxes show, or on a slow run the stack shifts under the pointer.
  const settled = () => page.locator(".toasts").evaluate((stack) => Promise.all(stack.getAnimations({ subtree: true }).map((animation) => animation.finished)));
  await page.clock.install();
  await open(page);
  await page.clock.pauseAt(await page.evaluate(() => Date.now()) + 1000);
  await settled();
  const dismiss = page.locator(".toasts .toast").first().getByRole("button", { name: /^Dismiss/ });
  await dismiss.hover();
  await expect(page.getByRole("tooltip")).toHaveText((await dismiss.getAttribute("data-tip"))!);
  const meetsNext = await dismiss.evaluate((element) => {
    const next = element.closest(".toast")!.nextElementSibling!.querySelector(".dialogue-box")!.getBoundingClientRect(), tip = document.querySelector("[role=tooltip]")!.getBoundingClientRect();
    return tip.bottom > next.top && tip.top < next.bottom && tip.right > next.left && tip.left < next.right;
  });
  expect(meetsNext, "the tip lies on the next alert").toBe(true);
  expect(await tipOnTop(page)).toBe(true);
});

test("with reduced motion the boxes just appear and Open Command Center keeps its glow without breathing", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await open(page);
  expect(await page.locator(".dialogue").evaluateAll((boxes) => boxes.map((element) => getComputedStyle(element).animationName))).toEqual(Array(4).fill("none"));
  await expect(command(page)).toHaveCSS("animation-name", "none");
  expect(await command(page).evaluate((button) => getComputedStyle(button).boxShadow)).not.toBe("none");
});

test("the CFO's bar wears the mark of the harness the CFO runs, at rest and while something waits, and no goblin's alert does", async ({ page }) => {
  await open(page);
  for (const mark of [waiting(page).locator(".mark"), resting(page).locator(".mark")]) {
    await expect(mark).toHaveAttribute("aria-label", "Claude Code · claude-opus-5-5");
    await expect(mark).toHaveAttribute("data-tip", "Claude Code · claude-opus-5-5");
  }
  await expect(page.locator(".toasts .mark")).toHaveCount(0);
});
