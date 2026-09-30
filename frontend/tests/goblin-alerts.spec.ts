import { expect, test, type Locator, type Page } from "@playwright/test";

const LANTERN = "rgb(242, 180, 71)", MOSS = "rgb(134, 179, 107)", EMBER = "rgb(216, 102, 76)";

// Headless Chromium reports notifications as denied, so this browser starts as
// one the board has never asked, which is when it offers them.
async function open(page: Page, query = "") {
  await page.addInitScript(() => Object.defineProperty(Notification, "permission", { get: () => "default" }));
  await page.goto("/tests/fixtures/goblin-alerts.html" + query);
  await expect(page.getByRole("group", { name: "Alerts" }).or(page.locator(".toasts"))).toBeVisible();
  await expect(page.locator(".toasts .dialogue")).toHaveCount(4);
}
const box = (page: Page, text: string) => page.locator(".dialogue").filter({ hasText: text });
const tab = (dialogue: Locator) => dialogue.locator(".dialogue-tab");
const filled = (button: Locator) => button.evaluate((element) => getComputedStyle(element, "::before").backgroundColor);

test("the CFO's banner offers the Command Center, filled only while something waits, and its terminal as an icon", async ({ page }) => {
  await open(page);
  const needs = box(page, "Waiting on you"), quiet = box(page, "All quiet.");
  await expect(tab(needs)).toHaveText("CFO");
  await expect(tab(needs)).toHaveCSS("color", LANTERN);
  await expect(tab(quiet)).toHaveCSS("color", MOSS);
  expect(await filled(needs.getByRole("button", { name: "Open Command Center" }))).toBe(LANTERN);
  expect(await filled(quiet.getByRole("button", { name: "Open Command Center" }))).toBe("rgba(0, 0, 0, 0)");
  await needs.locator(".dialogue-actions").getByRole("button", { name: "Open the CFO's terminal" }).click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its icon");
  await needs.locator("button.dialogue-portrait").click();
  await expect(page.locator("output")).toHaveText("opened the CFO's terminal from its portrait");
});

test("Open Command Center opens the first item waiting, or the inbox when nothing waits", async ({ page }) => {
  await open(page);
  await box(page, "Waiting on you").getByRole("button", { name: "Open Command Center" }).click();
  await expect(page.locator("dialog.question-modal")).toBeVisible();
  await expect(page.locator("dialog.question-modal")).toContainText("Pick the waveform");
  await open(page, "?nothing");
  await box(page, "All quiet.").getByRole("button", { name: "Open Command Center" }).click();
  await expect(page.locator("details.command-center-menu")).toHaveAttribute("open", "");
  await expect(page.getByText("Nothing is waiting on you.")).toBeVisible();
  await expect(page.locator("dialog.question-modal")).toBeHidden();
});

test("each alert is its speaker's box: what needs him opens the Command Center, a goblin's news opens the goblin", async ({ page }) => {
  await open(page);
  const cases: [string, string, string, string, boolean][] = [
    ["cg-board-kill asks: Which layout should I keep?", "cg-board-kill", LANTERN, "Open Command Center", true],
    ["cg-board-kill finished: code-goblins #204 is ready.", "cg-board-kill", MOSS, "Open cg-board-kill", false],
    ["pd-billing-admin failed: its checks failed", "pd-billing-admin", EMBER, "Open pd-billing-admin", false],
  ];
  for (const [text, speaker, color, action, isFilled] of cases) {
    const alert = page.locator(".toasts .dialogue").filter({ hasText: text });
    await expect(tab(alert)).toHaveText(speaker);
    await expect(tab(alert)).toHaveCSS("color", color);
    expect(await filled(alert.getByRole("button", { name: action })), text).toBe(isFilled ? LANTERN : "rgba(0, 0, 0, 0)");
  }
  await expect(page.getByRole("button", { name: /terminal/i }).filter({ hasText: /Open terminal/ })).toHaveCount(0);
  await page.locator(".toasts .dialogue").filter({ hasText: "finished" }).getByRole("button", { name: "Open cg-board-kill" }).click();
  await expect(page.locator("output")).toHaveText("opened cg-board-kill");
  const ask = page.locator(".toasts .dialogue").filter({ hasText: "Windows notification" });
  await expect(tab(ask)).toHaveText("CFO");
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
  await expect(button).toHaveCSS("outline-width", "2px");
});

test("with reduced motion the boxes just appear", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await open(page);
  expect(await page.locator(".dialogue").evaluateAll((boxes) => boxes.map((element) => getComputedStyle(element).animationName))).toEqual(Array(6).fill("none"));
});
