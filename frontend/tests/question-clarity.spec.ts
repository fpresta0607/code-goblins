import { expect, test, type Locator, type Page, type Route } from "./site";

// The Overlord, 2026-11-02, of a goblin's question in his Command Center:
// "difficult to read, poorly formatted and full of gibberish". A question
// leads with its decision, the recommendation and what each choice does, and
// its asker puts run IDs, finding codes and receipts after a line that reads
// "Details:", which the card folds under Technical details. A question
// written before that line existed, such as the one in his screenshot, shows
// whole and wrapped; nothing rewrites it.

const RUN_ID = "01M6EASKNZ3NNDFC6PWCBFHY7R";
const LOG_LINK = "https://github.com/example/project/actions/runs/38280093670/job/112233445566778899?check_suite_focus=true";
const SCREENSHOT_END = "the separate CEO lock-only/read-only restriction is why this new scope needs your decision.";

async function openCard(page: Page, name: string) {
  await page.goto("/tests/fixtures/question-clarity.html");
  await page.getByRole("button", { name, exact: true }).click();
  const card = page.getByRole("dialog").locator(".question-card");
  await expect(card).toBeVisible();
  return card;
}

// record answers every action the card sends, as the supervisor queues it.
async function record(page: Page) {
  const sent: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route: Route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    sent.push(body);
    await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  });
  return sent;
}

// overflow is how far the page, the Command Center's scroller or the card
// reach past their own width.
const overflow = (page: Page) => page.evaluate(() => [document.documentElement, document.querySelector(".command-center-content"), document.querySelector(".question-card")]
  .map((element) => element ? element.scrollWidth - element.clientWidth : -1));

// expectFolded checks the disclosure as a screen reader meets it: a
// disclosure triangle named Technical details, expanded or not.
async function expectFolded(page: Page, details: Locator, isOpen: boolean) {
  await expect(details).toHaveJSProperty("open", isOpen);
  const session = await page.context().newCDPSession(page);
  try {
    const { result } = await session.send("Runtime.evaluate", { expression: "document.querySelector('.question-details > summary')" });
    const { nodes: [node] } = await session.send("Accessibility.getPartialAXTree", { objectId: result.objectId, fetchRelatives: false });
    expect(node.role?.value).toBe("DisclosureTriangle");
    expect(node.name?.value).toBe("Technical details");
    expect(node.properties?.find((property) => property.name === "expanded")?.value.value).toBe(isOpen);
  } finally { await session.detach(); }
}

for (const width of [320, 375, 1440]) {
  test(`the screenshot's question shows whole and wrapped at ${width}px, with nothing folded or rewritten`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: width > 600 ? 1000 : 740 });

    // Act
    const card = await openCard(page, "Open the screenshot question");

    // Assert
    const body = card.locator(".question-body");
    await expect(body).toContainText("Authorize one managed round for the three new Review findings?");
    await expect(body).toContainText("lifecycle-selected01M6EASKNZ3NNDFC6PWCBFHY7R is awaiting_approval");
    await expect(body).toContainText(SCREENSHOT_END);
    await expect(body.getByRole("listitem")).toHaveCount(8);
    await expect(card.locator(".question-details")).toHaveCount(0);
    await expect(card.getByRole("radio", { name: /Authorize managed review repair/ })).toBeVisible();
    expect(await overflow(page)).toEqual([0, 0, 0]);
    await page.screenshot({ path: testInfo.outputPath(`screenshot-question-${width}.png`) });
  });

  test(`a decision with Details shows its question, recommendation and choices' consequences and folds its evidence at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: width > 600 ? 1000 : 740 });
    const card = await openCard(page, "Open the clear question");
    const details = card.locator(".question-details");

    // Act
    await expectFolded(page, details, false);
    await page.screenshot({ path: testInfo.outputPath(`clear-question-folded-${width}.png`) });
    await details.locator("> summary").click();

    // Assert
    const decision = card.locator(".question-body").first();
    await expect(decision).toContainText("Fix the three review findings in one managed round?");
    await expect(decision.locator("strong")).toHaveText("Recommended: fix them now.");
    await expect(decision).toContainText("Keep Review parked: nothing changes, the pull request stays held at Review and the skipped-test bug stays.");
    await expect(decision).toContainText("Not known yet: whether the run's remaining review allowance covers this round.");
    await expect(decision).not.toContainText(RUN_ID);
    await expect(page.getByRole("form", { name: /^Fix the three review findings in one managed round\?/ })).not.toHaveAccessibleName(new RegExp(RUN_ID));
    await expectFolded(page, details, true);
    const evidence = details.locator(".question-evidence");
    await expect(evidence.getByRole("listitem")).toHaveCount(7);
    await expect(evidence.getByRole("button", { name: "Copy " + RUN_ID })).toBeVisible();
    await expect(evidence.getByRole("link", { name: LOG_LINK })).toHaveAttribute("href", LOG_LINK);
    expect(await overflow(page)).toEqual([0, 0, 0]);
    await page.screenshot({ path: testInfo.outputPath(`clear-question-open-${width}.png`), fullPage: true });
  });
}

test("Enter and Space open and close the evidence, and a screen reader hears its state", async ({ page }) => {
  // Arrange
  const card = await openCard(page, "Open the clear question");
  const details = card.locator(".question-details");
  const summary = details.locator("> summary");

  // Act
  await summary.press("Enter");

  // Assert
  await expectFolded(page, details, true);
  await expect(details.getByRole("button", { name: "Copy " + RUN_ID })).toBeVisible();
  await summary.press("Space");
  await expectFolded(page, details, false);
  await expect(details.getByRole("button", { name: "Copy " + RUN_ID })).toHaveCount(0);
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 375, height: 667 }, hasTouch: true, isMobile: true });

  test("a tap opens the evidence and the Command Center scrolls inside while the page stays still", async ({ page }, testInfo) => {
    // Arrange
    const card = await openCard(page, "Open the clear question");
    const details = card.locator(".question-details");
    const content = page.locator(".command-center-content");
    const dialog = page.getByRole("dialog");

    // Act
    await details.locator("> summary").tap();
    await card.getByRole("button", { name: "Send decision" }).scrollIntoViewIfNeeded();

    // Assert
    await expectFolded(page, details, true);
    await expect(card.getByRole("button", { name: "Send decision" })).toBeInViewport();
    expect(await content.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
    expect(await dialog.evaluate((element) => element.scrollTop)).toBe(0);
    expect(await page.evaluate(() => window.scrollY)).toBe(0);
    expect(await overflow(page)).toEqual([0, 0, 0]);
    await page.screenshot({ path: testInfo.outputPath("clear-question-phone-scrolled.png") });
  });
});

test("his choice, the open evidence and Skip survive a new snapshot, and Send answers the question it shows", async ({ page }) => {
  // Arrange
  const sent = await record(page);
  const card = await openCard(page, "Open the clear question");
  const details = card.locator(".question-details");
  await card.getByRole("radio", { name: /Keep Review parked/ }).check();
  await details.locator("> summary").click();

  // Act
  await page.evaluate(() => window.refresh?.());
  await page.evaluate(() => window.refresh?.());

  // Assert
  await expect(card.getByRole("radio", { name: /Keep Review parked/ })).toBeChecked();
  await expectFolded(page, details, true);
  await expect(card.getByRole("button", { name: "Skip this card" })).toBeEnabled();
  await details.locator("> summary").click();
  await expect(card.getByRole("radio", { name: /Keep Review parked/ })).toBeChecked();
  await card.getByRole("button", { name: "Send decision" }).click();
  await expect.poll(() => sent).toEqual([expect.objectContaining({ kind: "ceo_answer", question_id: "ci-review-repair", generation: "b".repeat(64), answer_kind: "option", text: "Keep Review parked" })]);
});

test("markup in a question's decision and evidence is shown as text and never runs", async ({ page }) => {
  // Arrange
  const card = await openCard(page, "Open the hostile question");

  // Act
  await card.locator(".question-details > summary").click();

  // Assert
  await expect(card.locator(".question-body").first()).toContainText("Keep <b>the</b> <img src=x onerror=\"window.hacked=1\"> page?");
  await expect(card.locator(".question-body").first()).toContainText("<script>window.hacked=2</script>");
  await expect(card.locator(".question-evidence")).toContainText("<img src=x onerror=\"window.hacked=3\"> and <script>window.hacked=4</script>");
  await expect(card.locator("img, script, b")).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { hacked?: number }).hacked)).toBeUndefined();
});

test("an answered question opened from History folds its evidence the same way and keeps its answer", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/question-clarity.html");
  await page.locator(".command-center-menu > summary").click();
  await page.locator(".command-center-updates").getByText("History").click();

  // Act
  await page.getByRole("button", { name: /^Change the answer to .*Move the nightly import to 02:00\?/ }).click();

  // Assert
  const card = page.getByRole("dialog").locator(".change-card");
  await expect(card.locator(".question-body").first()).toContainText("Recommended: move it.");
  await expect(card.locator(".question-body").first()).not.toContainText("nightly-backup-7f3a");
  await expect(card.getByRole("radio", { name: /Move it/ })).toBeChecked();
  await card.locator(".question-details > summary").click();
  await expectFolded(page, card.locator(".question-details"), true);
  await expect(card.locator(".question-evidence").getByRole("button", { name: "Copy nightly-backup-7f3a" })).toBeVisible();
});
