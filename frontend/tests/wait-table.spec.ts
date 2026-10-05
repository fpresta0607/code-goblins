import { expect, test } from "./site";

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

// The board is opened across the tailnet over plain HTTP, where a page has no
// navigator.clipboard, so a value is copied the older way instead.
test("a value still copies with one click on a page that has no clipboard", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => {
    Object.defineProperty(Navigator.prototype, "clipboard", { value: undefined, configurable: true });
    document.addEventListener("copy", () => {
      const field = document.activeElement;
      (window as unknown as { copiedValue: string }).copiedValue = field instanceof HTMLTextAreaElement ? field.value.slice(field.selectionStart, field.selectionEnd) : "";
    });
  });
  await page.goto("/tests/fixtures/wait-table.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const button = page.getByRole("dialog").getByRole("button", { name: "Copy 9fQe2kLx7Rm0aPz4Vb8Nw" });

  // Act
  await button.click();

  // Assert
  await expect(button).toHaveAttribute("data-tip", "Copied");
  expect(await page.evaluate(() => (window as unknown as { copiedValue: string }).copiedValue)).toBe("9fQe2kLx7Rm0aPz4Vb8Nw");
  await expect(button).toBeFocused();
  await expect(page.locator("textarea")).toHaveCount(0);
});

// A copy label that opened below its button reached past the last row, so the
// table grew a scrollbar of its own and cut the label off.
test("a wait's table never scrolls up or down inside itself, so its last row's copy label shows whole", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/wait-table.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const dialog = page.getByRole("dialog");
  const button = dialog.getByRole("button", { name: "Copy 2a09:8280:1::4f:2c1a" });

  // Act
  await button.hover();
  const scrolled = await dialog.locator(".message-table").evaluate((box) => {
    box.scrollTop = 100;
    return box.scrollTop;
  });

  // Assert
  await expect(button).toHaveAttribute("data-tip", "Copy");
  expect(scrolled).toBe(0);
});

// The Overlord opens the board on his phone, where a card whose actions could
// not wrap grew wider than the screen and took its heading and half its copy
// buttons past the edge. The table alone scrolls sideways, since four columns
// squeezed into a phone's width read one letter to a line.
test("a wait's table stays inside its card at phone width, with every copy button in reach", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/tests/fixtures/wait-table.html");
  const edges = (box: Element) => {
    const { left, right } = box.getBoundingClientRect();
    return { left, right };
  };

  // Act
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const dialog = page.getByRole("dialog");
  const card = await dialog.locator(".question-card").evaluate(edges);
  const buttons = await dialog.getByRole("table").getByRole("button", { name: /^Copy / }).all();

  // Assert
  expect(buttons).toHaveLength(6);
  expect(card.right).toBeLessThanOrEqual(await dialog.evaluate((box) => box.getBoundingClientRect().right));
  for (const part of [dialog.getByRole("heading", { name: /Add these three DNS records/ }), dialog.locator(".asker"), dialog.locator(".message-table"), dialog.locator(".card-actions")]) {
    const inside = await part.evaluate(edges);
    expect(inside.left).toBeGreaterThanOrEqual(card.left);
    expect(inside.right).toBeLessThanOrEqual(card.right);
  }
  for (const button of buttons) {
    await button.scrollIntoViewIfNeeded();
    const inside = await button.evaluate(edges);
    expect(inside.left).toBeGreaterThanOrEqual(card.left);
    expect(inside.right).toBeLessThanOrEqual(card.right);
    expect(await dialog.evaluate((box) => box.scrollLeft)).toBe(0);
  }
  expect(await dialog.locator(".message-table code").evaluateAll((values) => Math.max(...values.map((value) => value.getClientRects().length)))).toBeLessThanOrEqual(3);
  expect(await dialog.locator(".message-table").evaluate((box) => {
    box.scrollTop = 100;
    return box.scrollTop;
  })).toBe(0);
});

test("a wait's row in the menu reads the goblin's words alone, on one line", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/wait-table.html");

  // Act
  await page.getByLabel("Command Center, 3 waiting on you").click();

  // Assert
  await expect(page.locator(".inbox-summary")).toHaveText([
    "Add these three DNS records in Cloudflare for precisiondocs.ai, then tell me So mcp.precisiondocs.ai serves the connector.",
    "check that https://mcp.precisiondocs.ai answers, then tell me",
    "Add this record, then tell me",
  ]);
});

test("a wait that opens with its table shows the table, with no row of pipes as its heading", async ({ page }) => {
  // Arrange
  await page.goto("/tests/fixtures/wait-table.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const dialog = page.getByRole("dialog");

  // Act
  await dialog.getByRole("button", { name: "Next item" }).click();
  await dialog.getByRole("button", { name: "Next item" }).click();

  // Assert
  await expect(dialog.getByRole("table").getByRole("columnheader")).toHaveText(["Type", "Name"]);
  await expect(dialog.getByRole("table").getByRole("button", { name: "Copy mail" })).toBeVisible();
  await expect(dialog.getByText("Add this record, then tell me")).toBeVisible();
  await expect(dialog.getByText("|")).toHaveCount(0);
});
