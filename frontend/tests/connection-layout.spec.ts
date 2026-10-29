import { expect, test, type Page } from "./site";

const longName = "Repository observability and deployment notifications for the production project";
const entries = [
  { id: "service:long", name: longName, kind: "service", status: "connected", checked_at: "2026-10-02T23:45:00Z", grant: "revoke" },
  { id: "service:context7", name: "Context7", kind: "service", status: "unauthorized", detail: "Sign-in required.", actions: ["login"], checked_at: "2026-10-02T23:45:00Z", grant: "revoke" },
];

async function open(page: Page, width: number, isChecking = false) {
  await page.route("**/api/connections**", (route) => route.fulfill({ json: { instance: "layout-proof", checking: isChecking, entries: isChecking ? entries.map((entry) => ({ ...entry, status: "checking", checked_at: "" })) : entries } }));
  await page.goto("/tests/fixtures/connection-layout.html");
  await page.locator("main").evaluate((element, width) => {
    const style = getComputedStyle(element);
    element.style.width = width + parseFloat(style.paddingLeft) + parseFloat(style.paddingRight) + "px";
  }, width);
  await expect(page.locator(".connector-card")).toHaveCount(2);
}

for (const width of [720, 520, 360]) {
  test(`a card's long name wraps whole before its buttons, with no tooltip, at a panel width of ${width}`, async ({ page }) => {
    await open(page, width);
    const card = page.locator(".connector-card").first();
    const name = (await card.locator("strong").boundingBox())!;
    const button = (await card.locator(".connector-toggle").boundingBox())!;
    expect(name.x + name.width).toBeLessThan(button.x);
    await expect(card.locator("strong")).toHaveText(longName);
    expect(await card.locator("strong").evaluate((element) => element.scrollWidth <= element.clientWidth + 1 && element.scrollHeight <= element.clientHeight + 1)).toBe(true);
    await card.locator("strong").hover();
    await expect(page.getByRole("tooltip")).toHaveCount(0);
    const panel = page.getByRole("region", { name: "Connections", exact: true });
    expect(await panel.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await expect(page.getByRole("button", { name: "Sign in to Context7" })).toBeVisible();
  });
}

test("a card being checked shows no trouble until its check ends, and keeps its button", async ({ page }) => {
  await open(page, 720, true);
  await expect(page.getByText("Checking connections...", { exact: true })).toBeVisible();
  await expect(page.locator(".connector-trouble")).toHaveCount(0);
  await expect(page.locator(".connector-card .connector-toggle")).toHaveCount(2);
});
