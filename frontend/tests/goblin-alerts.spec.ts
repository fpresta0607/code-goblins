import { expect, test, type Locator, type Page } from "./site";

const LANTERN = "rgb(242, 180, 71)";

// Headless Chromium reports notifications as denied, so this browser starts as
// one the board has never asked, which is when it offers them.
async function open(page: Page) {
  await page.addInitScript(() => Object.defineProperty(Notification, "permission", { get: () => "default" }));
  await page.goto("/tests/fixtures/goblin-alerts.html");
  await expect(page.getByRole("group", { name: "Alerts" }).or(page.locator(".toasts"))).toBeVisible();
  await expect(page.locator(".toasts .dialogue")).toHaveCount(1);
}
// The fixture's two bars: one over a board column while a question waits on
// the Overlord, the CFO's lantern box, and one at rest.
const waiting = (page: Page) => page.locator(".task-board .cfo-pin .dialogue");
const resting = (page: Page) => page.locator("main > .cfo-pin .cfo-rest");
const command = (page: Page) => waiting(page).getByRole("button", { name: "Open Command Center: 1 waiting on you" });
const filled = (button: Locator) => button.evaluate((element) => getComputedStyle(element, "::before").backgroundColor);

// The Overlord, 2026-10-07, after the bar's gold Open Command Center pill: "i
// liked the lantern box button". While something waits on him the bar is the
// CFO's lantern box again, as before 2026-10-02, with the count on its button.
test("the CFO's bar is a plain card at rest and the CFO's lantern box while something waits, whose lantern Open Command Center counts what waits and says none of it", async ({ page }) => {
  await open(page);
  const needs = waiting(page), rest = resting(page);
  // The lantern box, with the CFO's name on its tab and none of the alert's
  // words: the line says only how many goblins the CFO supervises, and the
  // button how many things wait.
  await expect(needs.locator(".dialogue-tab")).toHaveText("CFO");
  await expect(needs.locator(".dialogue-tab")).toHaveCSS("color", LANTERN);
  await expect(needs.locator(".dialogue-text p")).toHaveText("3 goblins at work.");
  await expect(page.locator(".task-board .cfo-pin")).not.toContainText("Which layout should I keep?");
  await expect(command(page)).toHaveText("Open Command Center1");
  await expect(command(page).locator(".cfo-command-count")).toHaveText("1");
  expect(await filled(command(page))).toBe(LANTERN);
  await expect(command(page)).toHaveCSS("animation-name", "none");
  await needs.locator(".dialogue-actions").getByRole("button", { name: "Open the CFO's terminal" }).click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its icon");
  await needs.locator("button.dialogue-portrait").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its portrait");
  // At rest: no lantern box and no Open Command Center; only its terminal, on
  // the icon and the portrait, on the same card as the column under it.
  await expect(rest).toContainText("All quiet. 3 goblins at work.");
  await expect(page.locator("main > .cfo-pin .dialogue")).toHaveCount(0);
  await expect(rest.getByRole("button")).toHaveCount(2);
  await expect(rest.getByRole("button", { name: /Open Command Center/ })).toHaveCount(0);
  const look = (locator: Locator) => locator.evaluate((element) => { const style = getComputedStyle(element); return [style.backgroundColor, style.borderTopColor, style.borderRadius].join(" "); });
  expect(await look(rest)).toBe(await look(page.locator(".board-column")));
  await rest.locator("button.icon-button").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its icon");
  await rest.locator("button.cfo-rest-portrait").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its portrait");
});

test("the bar keeps its line and its buttons inside it on a phone, at rest and while something waits", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  for (const bar of [resting(page), waiting(page).locator(".dialogue-box")]) {
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

// The Overlord, 2026-10-07: "alerts should only be open command center
// questions". A goblin finishing or failing, and its question for the CFO,
// are on its card; the board shows no toast for them, nor for the CFO's own
// question, whose one signal is the bar's Open Command Center. The first
// item for him brings only the ask for Windows notifications.
test("a goblin's news and its question for the CFO show no toast, and the CFO's question brings only the ask for notifications", async ({ page }, testInfo) => {
  await open(page);
  await expect(page.locator(".toasts .toast")).toHaveCount(1);
  for (const said of ["cg-board-kill", "pd-billing-admin", "cg-voice", "Which layout should I keep?", "Merge pull request 204 now?"]) await expect(page.locator(".toasts"), said).not.toContainText(said);
  await expect(page.locator("dialog.question-modal")).not.toBeVisible();
  await testInfo.attach("cfo-question-after", { body: await page.screenshot(), contentType: "image/png" });
  await expect(page.getByRole("button", { name: /terminal/i }).filter({ hasText: /Open terminal/ })).toHaveCount(0);
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

test("the stepped frames clip no focus ring or tooltip, and a focused Open Command Center on the bar shows its outline", async ({ page }) => {
  await open(page);
  const clipped = await page.locator(".dialogue").evaluateAll((boxes) => boxes.flatMap((dialogue) =>
    [...dialogue.querySelectorAll("button, a[href], input, select, textarea, [tabindex], [data-tip]")].flatMap((element) => {
      const chain: Element[] = [];
      for (let node: Element | null = element; node && node !== dialogue.parentElement; node = node.parentElement) chain.push(node);
      return chain.filter((node) => getComputedStyle(node).clipPath !== "none").map((node) => node.className);
    })));
  expect(clipped).toEqual([]);
  // No alert carries Open Command Center: an item's one signal is the bar's
  // button.
  await expect(page.locator(".toasts").getByRole("button", { name: "Open Command Center" })).toHaveCount(0);
  await command(page).focus();
  await expect(command(page)).toHaveCSS("outline-style", "solid");
  await expect(command(page)).toHaveCSS("outline-width", "3px");
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
  const portrait = waiting(page).locator("button.dialogue-portrait");
  await portrait.hover();
  await expect(page.getByRole("tooltip")).toHaveText((await portrait.getAttribute("data-tip"))!);
  expect(await tipOnTop(page)).toBe(true);
});

test("with reduced motion the boxes just appear", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await open(page);
  expect(await page.locator(".dialogue").evaluateAll((boxes) => boxes.map((element) => getComputedStyle(element).animationName))).toEqual(["none", "none"]);
});

test("the CFO's bar wears the mark of the harness the CFO runs, at rest and while something waits, and no goblin's alert does", async ({ page }) => {
  await open(page);
  for (const mark of [waiting(page).locator(".mark"), resting(page).locator(".mark")]) {
    await expect(mark).toHaveAttribute("aria-label", "Claude Code · claude-opus-5-5");
    await expect(mark).toHaveAttribute("data-tip", "Claude Code · claude-opus-5-5");
  }
  await expect(page.locator(".toasts .mark")).toHaveCount(0);
});

// The Overlord, 2026-10-07: "with lantern bars back make sure alerts match it
// correct?". What waits for him wears the lantern box: the Command Center's
// count, its rows of what waits, and an item's card with its one action.
test("the Command Center's count, its waiting rows and an item's card wear the lantern box", async ({ page }) => {
  // Arrange
  await open(page);
  const HIDE = "rgb(36, 28, 21)";
  const before = (locator: Locator) => locator.evaluate((element) => { const style = getComputedStyle(element, "::before"); return [style.backgroundColor, style.clipPath.startsWith("polygon")].join(" "); });

  // Assert: the count on the Command Center's button.
  const badge = page.locator(".command-center-menu > summary .count-badge");
  await expect(badge).toHaveCSS("background-color", LANTERN);

  // Act
  await page.locator(".command-center-menu > summary").click();

  // Assert: each row of what waits, and its count.
  const row = page.locator(".inbox-list.waiting li").first();
  expect(await before(row)).toBe(HIDE + " true");
  await expect(page.locator(".command-center-updates h3 .column-count").first()).toHaveCSS("background-color", LANTERN);

  // Act
  await row.getByRole("button", { name: /^Answer / }).click();

  // Assert: the item's card and its action.
  const card = page.locator("dialog.question-modal .question-card");
  expect(await before(card)).toBe(HIDE + " true");
  expect(await filled(card.locator("button.primary"))).toBe(LANTERN);
});
