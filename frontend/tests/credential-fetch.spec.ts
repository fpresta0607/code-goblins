import { expect, test, type Page } from "./site";

// The key card's Fetch it for me: when a reviewed key page reads a key the
// request still needs, the card offers to fetch it above the paste fields,
// asks the board to on his click, shows the fetch running, says why one
// stopped, and once the key is stored its row says it was fetched.
const STRIPE = "cred-0123456789abcdef";
const offer = { provider: "Stripe", login: "PrecisionDocs", names: ["STRIPE_SECRET_KEY"], state: "", reason: "" };

const update = (page: Page, changes: Record<string, unknown>) => page.evaluate(([id, value]) => window.board!.request(id as string, value as Record<string, unknown>), [STRIPE, changes] as const);

async function openCard(page: Page) {
  await page.goto("/tests/fixtures/credential-card.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const card = page.getByRole("dialog").locator(`[data-credential-request="${STRIPE}"]`);
  await expect(card).toBeVisible();
  return card;
}

for (const viewport of [{ width: 1280, height: 1000 }, { width: 390, height: 844 }]) {
  test("the key card fetches a reviewed page's key on his click and says how it went, at width " + viewport.width, async ({ page }) => {
    await page.setViewportSize(viewport);
    const sent: { token: string | null; body: unknown }[] = [];
    await page.route("**/api/credentials/fetch", async (route) => {
      sent.push({ token: await route.request().headerValue("X-CFO-Token"), body: route.request().postDataJSON() });
      await route.fulfill({ status: 202, json: { state: "fetching" } });
    });
    const card = await openCard(page);
    await expect(card.locator(".credential-fetch")).toHaveCount(0);

    await update(page, { fetch: offer });
    const fetch = card.locator(".credential-fetch");
    await expect(fetch.getByText("The fleet can read STRIPE_SECRET_KEY from Stripe's key page with the PrecisionDocs login, straight into the vault. No agent sees it.", { exact: true })).toBeVisible();
    expect(await fetch.evaluate((element) => element.compareDocumentPosition(element.parentElement!.querySelector(".credential-table")!) & Node.DOCUMENT_POSITION_FOLLOWING)).toBeTruthy();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await card.screenshot({ path: "test-results/credential-fetch-" + viewport.width + ".png" });

    await fetch.getByRole("button", { name: "Fetch it for me" }).click();
    await expect.poll(() => sent.length).toBe(1);
    expect(sent[0]).toEqual({ token: "fixture-token", body: { id: STRIPE, generation: "f".repeat(32) } });

    await update(page, { fetch: { ...offer, state: "fetching" } });
    await expect(fetch.getByRole("status")).toHaveText("Fetching from Stripe's page");
    await expect(fetch.getByRole("button", { name: "Fetch it for me" })).toHaveCount(0);

    await update(page, { fetch: { ...offer, reason: "the Stripe key page: the page changed" } });
    await expect(fetch.locator(".credential-fetch-stopped")).toContainText("Fetch stopped");
    await expect(fetch.locator(".credential-fetch-stopped .muted")).toHaveText("the Stripe key page: the page changed");
    await expect(fetch.getByRole("button", { name: "Fetch it for me" })).toBeEnabled();

    await update(page, { saved: ["STRIPE_SECRET_KEY"], fetched: ["STRIPE_SECRET_KEY"], fetch: offer });
    await expect(card.locator(".credential-fetch")).toHaveCount(0);
    await expect(card.locator('[data-credential-name="STRIPE_SECRET_KEY"] .credential-stored .muted')).toHaveText("fetched from the page");
  });
}
