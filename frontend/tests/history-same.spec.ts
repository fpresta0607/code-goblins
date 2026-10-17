import { expect, test, type Page } from "./site";

test.use({ locale: "en-US", timezoneId: "America/Chicago", deviceScaleFactor: 1.5 });

const NOW = new Date("2026-10-15T13:45:00Z");

async function history(page: Page) {
  await page.clock.install({ time: NOW });
  await page.goto("/tests/fixtures/history-same.html");
  await page.locator(".command-center-menu > summary").click();
  const panel = page.locator(".command-center-updates");
  await panel.getByText("History").click();
  return { panel, rows: panel.locator(".inbox-history li") };
}

// reopens records each card the board asks the supervisor to bring back, and
// answers it with status and body.
async function reopens(page: Page, status = 200, body: Record<string, unknown> = {}) {
  const posted: Record<string, unknown>[] = [];
  await page.route("**/api/credentials/reopen", async (route) => {
    const sent = route.request().postDataJSON() as Record<string, unknown>;
    posted.push(sent);
    await route.fulfill({ status, json: status === 200 ? { id: sent.id } : body });
  });
  return posted;
}

const MARKS: [string, RegExp][] = [
  ["May I file the push key card now?", /^The CFO answered$/],
  ["Tick the App Group for the share extension", /^Done$/],
  ["Move the sign-in screen out of test mode", /^Closed\./],
  ["Turn on time sensitive notifications", /^You wrote: The box was already ticked$/],
  ["Paste APNS_PRIVATE_KEY for sample-app", /^Saved APNS_PRIVATE_KEY/],
  ["Accept the new developer terms", /^Closed\./],
  ["Merge the release pull request?", /^You answered$/],
  ["Add the DNS records for the docs site", /^You wrote: Added both records$/],
  ["Publish release v0.5.2", /exit 0/],
  ["Which accent should the board use?", /^The CFO answered while you were away$/],
  ["Should the sign-in page keep its old link?", /^Superseded/],
  ["Sign in to Example for sample-app", /^Signed in to Example/],
];

for (const [width, height] of [[1707, 1067], [390, 844]]) {
  test(`every History row leads with the CFO's goblin, marked with how it ended, and has the pencil, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height });

    // Act
    const { panel, rows } = await history(page);
    await expect(rows).toHaveCount(MARKS.length);
    await panel.screenshot({ path: testInfo.outputPath(`history-same-${width}.png`), animations: "disabled" });

    // Assert
    for (const [text, label] of MARKS) {
      const row = rows.filter({ hasText: text });
      const mark = row.locator(".history-mark");
      await expect(mark, text).toHaveCount(1);
      await expect(mark.locator(".goblin-avatar.persona-cfo"), text).toBeVisible();
      await expect(mark.locator(".mark-badge"), text).toBeVisible();
      await expect(mark, text).toHaveAttribute("role", "img");
      await expect(mark, text).toHaveAttribute("aria-label", label);
      await expect(row.locator(".history-change"), text).toHaveCount(1);
      await expect(row.locator(".history-change .icon"), text).toBeVisible();
    }
    const leading = await rows.evaluateAll((items) => items.map((item) => item.firstElementChild?.classList.contains("history-mark")));
    expect(leading, "every row leads with the mark").toEqual(MARKS.map(() => true));
    for (const head of await rows.locator(".history-head").all()) {
      expect(await head.evaluate((element) => Math.round(element.getBoundingClientRect().height / parseFloat(getComputedStyle(element).lineHeight))), "each row stays one line").toBe(1);
    }
    expect(await panel.evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);
  });
}

test("the pencil on a question changes its answer", async ({ page }) => {
  // Arrange
  const posted = await reopens(page);
  const { rows } = await history(page);

  // Act
  await rows.filter({ hasText: "May I file the push key card now?" }).locator(".history-change").click();

  // Assert
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("radio", { name: "Yes" })).toBeChecked();
  await expect(dialog.locator(".answered-by")).toContainText("The CFO chose this");
  expect(posted).toHaveLength(0);
});

for (const [text, id, generation] of [
  ["Tick the App Group for the share extension", "cred-1111111111111111", "1".repeat(32)],
  ["Move the sign-in screen out of test mode", "cred-2222222222222222", "2".repeat(32)],
  ["Turn on time sensitive notifications", "cred-3333333333333333", "3".repeat(32)],
] as const) {
  test(`the pencil on a step card brings it back to redo: ${text}`, async ({ page }) => {
    // Arrange
    const posted = await reopens(page);
    const { rows } = await history(page);
    const pencil = rows.filter({ hasText: text }).locator(".history-change");
    await expect(pencil).toHaveAttribute("data-tip", "Bring back");

    // Act
    await pencil.click();
    await expect.poll(() => posted.length).toBe(1);
    await page.evaluate((card) => window.cardBack?.(card), id);

    // Assert
    expect(posted[0]).toEqual({ id, generation });
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(text);
    await expect(dialog.getByRole("button", { name: /Done|Dismiss/ }).first()).toBeEnabled();
  });
}

test("a card the supervisor will not bring back says why on its row", async ({ page }) => {
  // Arrange
  await reopens(page, 409, { error: "This card is withdrawn, so it does not come back." });
  const { rows } = await history(page);
  const row = rows.filter({ hasText: "Tick the App Group for the share extension" });

  // Act
  await row.locator(".history-change").click();

  // Assert
  await expect(row.locator(".click-feedback")).toHaveText("This card is withdrawn, so it does not come back.");
  await expect(page.getByRole("dialog")).toBeHidden();
});

test("bringing a card back sends once and waits for its new generation", async ({ page }) => {
  // Arrange
  const posted = await reopens(page);
  const { rows } = await history(page);
  const pencil = rows.filter({ hasText: "Tick the App Group for the share extension" }).locator(".history-change");

  // Act
  await pencil.click();
  await expect.poll(() => posted.length).toBe(1);
  await pencil.dispatchEvent("click");

  // Assert
  await expect(pencil).toHaveAttribute("aria-disabled", "true");
  await expect(page.getByRole("dialog")).toBeHidden();
  expect(posted).toHaveLength(1);
  await page.evaluate(() => window.cardBack?.("cred-1111111111111111"));
  await expect(page.getByRole("dialog")).toContainText("Tick the App Group for the share extension");
});

for (const [text, why] of [
  ["Paste APNS_PRIVATE_KEY for sample-app", "Its values are stored"],
  ["Accept the new developer terms", "It expired"],
  ["Add the DNS records for the docs site", "A page or a wait stays as it closed"],
  ["Publish release v0.5.2", "A command that ran stays as it ran"],
  ["Should the sign-in page keep its old link?", "It has no answer to change"],
  ["Sign in to Example for sample-app", "This sign-in has finished"],
] as const) {
  test(`the pencil on a row nothing can change says why and does nothing: ${text}`, async ({ page }) => {
    // Arrange
    const posted = await reopens(page);
    const { rows } = await history(page);
    const pencil = rows.filter({ hasText: text }).locator(".history-change");

    // Act
    await pencil.dispatchEvent("click");

    // Assert
    await expect(pencil).toHaveAttribute("aria-disabled", "true");
    await expect(pencil).toHaveAttribute("data-tip", why);
    await expect(pencil).toHaveAccessibleName(new RegExp("^Nothing to change: .*" + text.replace(/[?.]/g, "\\$&")));
    await expect(page.getByRole("dialog")).toBeHidden();
    expect(posted).toHaveLength(0);
  });
}
