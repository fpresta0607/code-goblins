import { expect, test } from "@playwright/test";

// The Overlord, 2026-09-28, on a waiting card that listed three Cloudflare DNS
// records as one paragraph with literal ** marks and offered Open the link
// for a host named in its prose: "this should be a clean copyable field for
// each in clean table". The card reads its lead with the bold kept, the
// records as a table whose values each copy with one click, and opens only the
// link the goblin gave.
test("a wait's values read as a table and each copies with one click", async ({ page, context }) => {
  // Arrange
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/tests/fixtures/wait-table.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const dialog = page.getByRole("dialog");
  const table = dialog.getByRole("table");

  // Act
  await table.getByRole("button", { name: "Copy 9fQe2kLx7Rm0aPz4Vb8Nw" }).click();

  // Assert
  await expect(dialog.getByRole("heading", { name: /Add these three DNS records in Cloudflare/ }).locator("strong")).toHaveText("Cloudflare");
  await expect(table.getByRole("columnheader")).toHaveText(["Type", "Name", "Content", "Proxy"]);
  await expect(table.getByRole("row")).toHaveCount(4);
  await expect(table.getByRole("button", { name: /^Copy / })).toHaveCount(6);
  await expect(table.getByRole("button", { name: "Copy 9fQe2kLx7Rm0aPz4Vb8Nw" })).toHaveAttribute("data-tip", "Copied");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("9fQe2kLx7Rm0aPz4Vb8Nw");
  await expect(dialog.getByText("So mcp.precisiondocs.ai serves the connector.")).toBeVisible();
  await expect(dialog.getByText("**")).toHaveCount(0);
  await expect(dialog.getByRole("link", { name: "Open the link" })).toHaveAttribute("href", "https://dash.cloudflare.com/precisiondocs/dns");
});

test("an address only named in a wait's words is never offered as its link", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/wait-table.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const dialog = page.getByRole("dialog");

  // Act
  await dialog.getByRole("button", { name: "Next item" }).click();

  // Assert
  await expect(dialog.getByRole("heading", { name: /check that https:\/\/mcp\.precisiondocs\.ai answers/ })).toBeVisible();
  await expect(dialog.getByRole("link", { name: "Open the link" })).toHaveCount(0);
});
