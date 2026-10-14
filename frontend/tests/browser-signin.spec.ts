import { doneCards, expect, recordDoneCards, servePages, test, type Page } from "./site";

const OPEN = "Open the sign-in window";
const update = (page: Page, changes: Record<string, unknown>) => page.evaluate((value) => window.signInFixture!.items(value), changes);
async function openCard(page: Page, origin = "") {
  await page.goto(origin + "/tests/fixtures/browser-signin.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const card = page.getByRole("dialog").locator('[data-browser-signin="signin-stripe"]');
  await expect(card).toBeVisible();
  return card;
}

test("the approved instructions open only the requested sign-in once and wait for item events", async ({ page }, testInfo) => {
  const sent: { method: string; token: string | null; body: unknown }[] = [];
  await page.route("**/api/browser-signins/open", async (route) => {
    const request = route.request();
    sent.push({ method: request.method(), token: await request.headerValue("X-CFO-Token"), body: request.postDataJSON() });
    await route.fulfill({ status: 202, json: { accepted: true } });
  });
  const card = await openCard(page);
  await expect(card.getByRole("heading")).toHaveText("Sign in to Stripe for PrecisionDocs-AI");
  await expect(card.getByRole("list", { name: "Which login" }).locator("li")).toHaveText(["Stripe", "PrecisionDocs account", "Kept for PrecisionDocs-AI"]);
  await expect(card.getByRole("list", { name: "How to sign in" }).locator("li")).toHaveCount(4);
  await expect(card.getByRole("list", { name: "How to sign in" }).locator("li")).toHaveText([
    "Press Open the sign-in window",
    "Sign in to Stripe there with your password, a passkey or a code from your phone",
    "If Stripe asks which account, pick PrecisionDocs account",
    "The window closes by itself once you are signed in, and the fetch carries on",
  ]);
  await expect(card.locator("input, iframe, a")).toHaveCount(0);
  await card.screenshot({ path: testInfo.outputPath("browser-signin.png") });
  expect(sent).toEqual([]);

  await card.getByRole("button", { name: OPEN }).evaluate((button: HTMLButtonElement) => { button.click(); button.click(); });

  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0]).toEqual({ method: "POST", token: "fixture-token", body: { id: "signin-stripe", generation: "generation-one" } });
  await expect(card.getByRole("button", { name: OPEN })).toBeDisabled();
  await expect(card.getByRole("status")).toContainText("Opening");
  await update(page, {});
  await expect(card.getByRole("button", { name: OPEN })).toBeDisabled();
  await update(page, { state: "opening" });
  await expect(card.getByRole("button", { name: OPEN })).toBeDisabled();
  await update(page, { state: "open" });
  await expect(card.getByRole("status")).toContainText("The Stripe window is open on your screen");
  await expect(card.getByRole("button", { name: OPEN })).toBeDisabled();
  await expect(page.getByRole("heading", { name: "Signed in", exact: true })).toHaveCount(0);
  expect(sent).toHaveLength(1);
});

test("a window that fails to open returns to ready with the broker's reason", async ({ page }) => {
  await page.route("**/api/browser-signins/open", (route) => route.fulfill({ status: 202, json: { accepted: true } }));
  const card = await openCard(page);
  await card.getByRole("button", { name: OPEN }).click();
  await update(page, { state: "ready", reason: "The sign-in window could not open. Try again." });
  await expect(card).toContainText("The sign-in window could not open. Try again.");
  await expect(card.getByRole("button", { name: OPEN })).toBeEnabled();
});

test("an accepted opening stays disabled when he skips away and comes back", async ({ page }) => {
  let count = 0;
  await page.route("**/api/browser-signins/open", (route) => { count++; return route.fulfill({ status: 202, json: { accepted: true } }); });
  const card = await openCard(page);
  await card.getByRole("button", { name: OPEN }).click();
  await expect.poll(() => count).toBe(1);
  await card.getByRole("button", { name: "Skip", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Review the billing page" })).toBeVisible();
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await expect(card.getByRole("button", { name: OPEN })).toBeDisabled();
  expect(count).toBe(1);
});

test("an API refusal leaves the reason beside an enabled retry", async ({ page }) => {
  let count = 0;
  await page.route("**/api/browser-signins/open", (route) => {
    count++;
    return route.fulfill(count === 1 ? { status: 409, json: { error: "This login is in use. Try again." } } : { status: 202, json: { accepted: true } });
  });
  const card = await openCard(page);
  await card.getByRole("button", { name: OPEN }).click();
  await expect(card).toContainText("This login is in use. Try again.");
  await expect(card.getByRole("button", { name: OPEN })).toBeEnabled();
  await card.getByRole("button", { name: OPEN }).click();
  await expect.poll(() => count).toBe(2);
  await expect(card.getByRole("button", { name: OPEN })).toBeDisabled();
});

test("a closed window shows the broker's reason and lets him open it again", async ({ page }) => {
  let count = 0;
  await page.route("**/api/browser-signins/open", (route) => { count++; return route.fulfill({ status: 202, json: { accepted: true } }); });
  const card = await openCard(page);
  await card.getByRole("button", { name: OPEN }).click();
  await update(page, { state: "open" });
  await update(page, { state: "ready", reason: "The window closed before Stripe showed you signed in. Open it again." });
  await expect(card).toContainText("The window closed before Stripe showed you signed in. Open it again.");
  await expect(card.getByRole("button", { name: OPEN })).toBeEnabled();
  await card.getByRole("button", { name: OPEN }).click();
  await expect.poll(() => count).toBe(2);
});

for (const state of ["signed_in", "expired"]) {
  test(`${state} completes from an item event and advances to the next card with honest history`, async ({ page }) => {
    await openCard(page);
    await recordDoneCards(page);
    await update(page, { state, reason: state === "expired" ? "The sign-in request expired" : "", closed_at: "2026-10-12T10:05:00Z" });
    await expect.poll(() => doneCards(page)).toEqual(expect.arrayContaining([expect.stringContaining(state === "signed_in" ? "Signed in" : "Closed")]));
    if (state === "expired") expect((await doneCards(page)).join(" ")).not.toContain("Signed in");
    await expect(page.getByRole("dialog").getByRole("heading", { name: "Review the billing page" })).toBeVisible();
    await expect(page).toHaveTitle("(1) Browser sign-in fixture");
    await page.getByRole("button", { name: "Close the Command Center" }).click();
    await page.getByLabel("Command Center, 1 waiting on you", { exact: true }).click();
    await page.getByText("History", { exact: false }).click();
    await expect(page.locator(".inbox-list").filter({ hasText: "Sign in to Stripe" })).toContainText(state === "signed_in" ? "Signed in" : "expired");
  });
}

test("a remote board has no sign-in button and a disconnected local board disables it", async ({ page, context }) => {
  await servePages(context, "http://board.example.test");
  const remote = await openCard(page, "http://board.example.test");
  await expect(remote.getByRole("button", { name: OPEN })).toHaveCount(0);
  await expect(remote).toContainText("on your PC");
  const local = await openCard(page);
  await page.evaluate(() => window.signInFixture!.connection(false));
  await expect(local.getByRole("button", { name: OPEN })).toBeDisabled();
});

test("the sign-in card fits a narrow viewport", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const card = await openCard(page);
  const button = card.getByRole("button", { name: OPEN });
  await expect(button).toBeVisible();
  expect(await card.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await expect(button).toBeInViewport();
  await page.getByRole("dialog").screenshot({ path: testInfo.outputPath("browser-signin-narrow.png") });
});
