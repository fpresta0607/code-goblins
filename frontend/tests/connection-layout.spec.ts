import { expect, test, type Page } from "./site";

const longName = "Repository observability and deployment notifications for the production project";
const entries = [
  { id: "mcp:long", name: longName, kind: "mcp", status: "connected", checked_at: "2026-10-02T23:45:00Z" },
  { id: "mcp:fix", name: "Context7", kind: "mcp", status: "unauthorized", detail: "Sign-in required.", actions: ["login"], checked_at: "2026-10-02T23:45:00Z" },
];

async function open(page: Page, width: number, isChecking = false) {
  await page.route("**/api/connections**", (route) => route.fulfill({ json: { instance: "layout-proof", checking: isChecking, entries: isChecking ? entries.map((entry) => ({ ...entry, status: "checking", checked_at: "" })) : entries } }));
  await page.goto("/tests/fixtures/connection-layout.html");
  await page.locator("main").evaluate((element, width) => {
    const style = getComputedStyle(element);
    element.style.width = width + parseFloat(style.paddingLeft) + parseFloat(style.paddingRight) + "px";
  }, width);
  await expect(page.locator(".connection-row")).toHaveCount(2);
}

for (const width of [720, 520, 519, 360]) {
  test(`connection name and status stay apart at a panel width of ${width}`, async ({ page }) => {
    await open(page, width);
    const row = page.locator(".connection-row").first();
    const name = (await row.locator("strong").boundingBox())!;
    const verdict = (await row.locator(".connection-verdict").boundingBox())!;
    if (width >= 520) {
      expect(Math.abs(name.y - verdict.y)).toBeLessThan(3);
      expect(name.x + name.width).toBeLessThan(verdict.x);
      await row.locator("strong").hover();
      await expect(page.getByRole("tooltip")).toHaveText(longName);
    } else {
      expect(verdict.y).toBeGreaterThanOrEqual(name.y + name.height);
      expect(verdict.x).toBe(name.x);
    }
    const panel = page.getByRole("region", { name: "Connections", exact: true });
    expect(await panel.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await expect(page.getByRole("button", { name: "Sign in to Context7" })).toBeVisible();
  });
}

for (const width of [720, 360]) {
  test(`checking rows have space between their content at ${width}`, async ({ page }) => {
    await open(page, width, true);
    const rows = page.locator(".connection-row");
    const before = (await rows.first().locator("time").boundingBox())!;
    const after = (await rows.nth(1).locator("strong").boundingBox())!;
    expect(after.y - before.y - before.height).toBeGreaterThanOrEqual(48);
    await expect(page.getByText("Checking connections...", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Sign in to Context7" })).toBeDisabled();
  });
}
