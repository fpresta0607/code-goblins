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
const box = (page: Page, text: string) => page.locator(".dialogue").filter({ hasText: text });
const tab = (dialogue: Locator) => dialogue.locator(".dialogue-tab");
const filled = (button: Locator) => button.evaluate((element) => getComputedStyle(element, "::before").backgroundColor);

test("the CFO's banner is a plain bar with its terminal and the AFK switch until something waits, and then the lantern box with Open Command Center", async ({ page }) => {
  await open(page);
  const needs = box(page, "Waiting on you"), rest = page.locator(".cfo-rest");
  await expect(tab(needs)).toHaveText("CFO");
  await expect(tab(needs)).toHaveCSS("color", LANTERN);
  expect(await filled(needs.getByRole("button", { name: "Open Command Center" }))).toBe(LANTERN);
  await needs.locator(".dialogue-actions").getByRole("button", { name: "Open the CFO's terminal" }).click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its icon");
  await needs.locator("button.dialogue-portrait").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its portrait");
  // At rest: the same card as the column under it, no lantern box, no name
  // tab and no Open Command Center; only its terminal, on the icon and the
  // portrait, and the two sides of the AFK switch.
  await expect(rest).toContainText("All quiet. The CFO supervises 3 goblins.");
  await expect(page.locator(".dialogue").filter({ hasText: "All quiet." })).toHaveCount(0);
  await expect(rest.getByRole("button")).toHaveCount(4);
  await expect(rest.getByRole("group", { name: "AFK mode" }).getByRole("button")).toHaveText(["Off", "On"]);
  await expect(rest.getByRole("button", { name: "Open Command Center" })).toHaveCount(0);
  const look = (locator: Locator) => locator.evaluate((element) => { const style = getComputedStyle(element); return [style.backgroundColor, style.borderTopColor, style.borderRadius].join(" "); });
  expect(await look(rest)).toBe(await look(page.locator(".board-column")));
  await rest.locator("button.icon-button").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its icon");
  await rest.locator("button.cfo-rest-portrait").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its portrait");
});

test("the bar at rest keeps its line and its button inside it on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  const inside = await page.locator(".cfo-rest").evaluate((bar) => {
    const box = bar.getBoundingClientRect();
    return [...bar.children].every((child) => { const part = child.getBoundingClientRect(); return part.left >= box.left && part.right <= box.right && part.top >= box.top && part.bottom <= box.bottom; });
  });
  expect(inside).toBe(true);
  expect(await page.locator(".cfo-rest p").evaluate((line) => parseFloat(getComputedStyle(line).fontSize))).toBeGreaterThanOrEqual(16);
});

test("Open Command Center opens the first item waiting", async ({ page }) => {
  await open(page);
  await box(page, "Waiting on you").getByRole("button", { name: "Open Command Center" }).click();
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
  // No name tab repeats who speaks: the box's words already say it.
  await expect(page.locator(".toasts .dialogue-tab")).toHaveCount(0);
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

test("the stepped frames clip no focus ring or tooltip, and a focused Open Command Center shows its outline", async ({ page }) => {
  await open(page);
  const clipped = await page.locator(".dialogue").evaluateAll((boxes) => boxes.flatMap((dialogue) =>
    [...dialogue.querySelectorAll("button, a[href], input, select, textarea, [tabindex], [data-tip]")].flatMap((element) => {
      const chain: Element[] = [];
      for (let node: Element | null = element; node && node !== dialogue.parentElement; node = node.parentElement) chain.push(node);
      return chain.filter((node) => getComputedStyle(node).clipPath !== "none").map((node) => node.className);
    })));
  expect(clipped).toEqual([]);
  const button = box(page, "Waiting on you").getByRole("button", { name: "Open Command Center" });
  await button.focus();
  await expect(button).toHaveCSS("outline-style", "solid");
  await expect(button).toHaveCSS("outline-width", "3px");
});

test("the banner's portrait tooltip shows over the board column under it", async ({ page }) => {
  await open(page);
  await page.addStyleTag({ content: ".dialogue-portrait[data-tip]::after { pointer-events: auto; }" });
  const portrait = box(page, "Waiting on you").locator("button.dialogue-portrait");
  await portrait.hover();
  const owner = await portrait.evaluate((element) => {
    const column = document.querySelector(".board-column")!.getBoundingClientRect(), face = element.getBoundingClientRect();
    const tip = getComputedStyle(element, "::after");
    const bottom = face.bottom + 8 + [tip.height, tip.paddingTop, tip.paddingBottom, tip.borderTopWidth, tip.borderBottomWidth].reduce((sum, size) => sum + parseFloat(size), 0);
    const x = face.left + 10, y = column.top + 4;
    if (y >= bottom) return "the tooltip does not reach the column";
    const hit = document.elementFromPoint(x, y);
    return hit && element.closest(".cfo-pin")!.contains(hit) ? "banner" : hit?.className ?? "nothing";
  });
  expect(owner).toBe("banner");
});

test("an alert's Dismiss tooltip shows over the next alert", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 });
  // Every alert leaves by itself after eight seconds, so time stands still once
  // the boxes show, or on a slow run the stack shifts under the pointer. The
  // boxes step in and the tooltip slides in, so each is measured once it stops.
  const settled = () => page.locator(".toasts").evaluate((stack) => Promise.all(stack.getAnimations({ subtree: true }).map((animation) => animation.finished)));
  await page.clock.install();
  await open(page);
  await page.clock.pauseAt(await page.evaluate(() => Date.now()) + 1000);
  await settled();
  await page.addStyleTag({ content: ".toasts [data-tip]::after { pointer-events: auto; }" });
  const dismiss = page.locator(".toasts .toast").first().getByRole("button", { name: /^Dismiss/ });
  await dismiss.hover();
  await settled();
  const owner = await dismiss.evaluate((element) => {
    const toast = element.closest(".toast")!, next = toast.nextElementSibling!.querySelector(".dialogue-box")!.getBoundingClientRect();
    const button = element.getBoundingClientRect(), tip = getComputedStyle(element, "::after");
    const top = button.bottom + 8, left = button.right - parseFloat(tip.width) - parseFloat(tip.paddingLeft) - parseFloat(tip.paddingRight);
    const x = Math.max(left, next.left) + 4, y = Math.max(top, next.top) + 4;
    if (x >= Math.min(button.right, next.right) || y >= next.bottom) return "the tooltip does not reach the next alert";
    const hit = document.elementFromPoint(x, y);
    return hit && toast.contains(hit) ? "alert" : hit?.className ?? "nothing";
  });
  expect(owner).toBe("alert");
});

test("with reduced motion the boxes just appear", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await open(page);
  expect(await page.locator(".dialogue").evaluateAll((boxes) => boxes.map((element) => getComputedStyle(element).animationName))).toEqual(Array(5).fill("none"));
});

test("the CFO's banner wears the mark of the harness the CFO runs, and no goblin's alert does", async ({ page }) => {
  await open(page);
  for (const mark of [box(page, "Waiting on you").locator(".dialogue-who [role=img]"), page.locator(".cfo-rest [role=img]")]) {
    await expect(mark).toHaveAttribute("aria-label", "Claude Code · claude-opus-5-5");
    await expect(mark).toHaveAttribute("data-tip", "Claude Code · claude-opus-5-5");
  }
  await expect(page.locator(".toasts .dialogue-who")).toHaveCount(0);
});
