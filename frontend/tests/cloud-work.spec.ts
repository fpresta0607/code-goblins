import { expect, test, type Page } from "./site";

const CLOUD = "https://github.com/o/r/pull/210";
const HEAD = "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00";

async function openItsRow(page: Page) {
  await page.goto("/tests/fixtures/cloud-work.html");
  await page.getByLabel("Command Center, 1 waiting on you").click();
  const row = page.locator(".inbox-list li.cloud-work-row");
  await expect(row).toHaveCount(1);
  return row;
}

// A handed-off goblin's cloud work waits on him as one Command Center item,
// naming the goblin, the pull request and its head, and for a teammate's that
// Bring back is his press; a pull request he left is not there.
test("cloud work ready names the goblin, the pull request and its head", async ({ page }) => {
  // Act
  const row = await openItsRow(page);

  // Assert
  await expect(row.locator("strong")).toHaveText("Cloud work ready for Parse headers");
  await expect(row.getByRole("link")).toHaveAttribute("href", CLOUD);
  await expect(row.getByRole("link")).toHaveText("Name the bad header, at c0ffee0, opened by teammate, not by you: Bring back is your press");
  await expect(row.getByRole("button", { name: "Bring back " + CLOUD + " onto Parse headers" })).toBeVisible();
  await expect(row.getByRole("button", { name: "Leave " + CLOUD })).toBeVisible();
});

// Bring back and Leave it each name the task's session and the head the row
// showed, with the board's token, so a push since is refused rather than
// brought back unseen.
for (const [button, label, action] of [["Bring back", /^Bring back /, "bring_back"], ["Leave it", /^Leave /, "leave"]] as const) {
  test(button + " names the head the row showed", async ({ page }) => {
    // Arrange
    let sent: { body: unknown; token: string | null } | null = null;
    await page.route("**/api/tasks/cloud", async (route) => {
      sent = { body: route.request().postDataJSON(), token: await route.request().headerValue("X-CEO-Token") };
      await route.fulfill({ status: 202, json: { accepted: true, revision: 2 } });
    });
    const row = await openItsRow(page);

    // Act
    await row.getByRole("button", { name: label }).click();

    // Assert
    await expect.poll(() => sent).not.toBeNull();
    expect(sent).toEqual({ body: { task: "cg-parser", generation: "s1", action, pull_request: CLOUD, head: HEAD }, token: "fixture" });
  });
}

// At a phone's width the row keeps its buttons on the screen, under its
// words, with no page scroll sideways.
test("cloud work ready fits a phone's width", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });

  // Act
  const row = await openItsRow(page);

  // Assert
  for (const label of [/^Bring back /, /^Leave /]) await expect(row.getByRole("button", { name: label })).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
