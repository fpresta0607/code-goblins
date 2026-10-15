import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-15 about 04:15Z: "history should have a little open
// icon and not be so much text just a short and sweet flex fit header". Each
// History row is one line that fits its width, cut short cleanly and never
// wrapping, and a small open icon unfolds the row's whole detail.
test.use({ locale: "en-US", timezoneId: "America/Chicago", deviceScaleFactor: 1.5 });

const NOW = new Date("2026-10-15T05:00:00Z");

async function history(page: Page) {
  await page.clock.install({ time: NOW });
  await page.goto("/tests/fixtures/history-rows.html");
  await page.locator(".command-center-menu > summary").click();
  const panel = page.locator(".command-center-updates");
  await panel.getByText("History").click();
  return { panel, rows: panel.locator(".inbox-history li") };
}

for (const [width, height] of [[1707, 1067], [390, 844]]) {
  test(`each History row is one short line with an open icon that unfolds its detail, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height });
    const { panel, rows } = await history(page);
    await expect(rows).toHaveCount(5);
    await panel.screenshot({ path: testInfo.outputPath(`history-${width}-folded.png`) });
    const heads = rows.locator(".history-head");
    const lines = (head: typeof heads) => head.evaluate((element) => Math.round(element.getBoundingClientRect().height / parseFloat(getComputedStyle(element).lineHeight)));
    await expect(heads).toHaveCount(5);
    for (const head of await heads.all()) {
      expect(await lines(head), "each folded row's header is one line").toBe(1);
      expect(await head.locator(".inbox-summary").evaluate((element) => getComputedStyle(element).textOverflow)).toBe("ellipsis");
    }
    await expect(rows.locator(".history-detail:visible")).toHaveCount(0);
    const hank = rows.filter({ hasText: "Add the DNS records" });
    const open = hank.getByRole("button", { name: /^Open: Hank - Record Keeper: .*Add the DNS records/ });
    await expect(open).toHaveAttribute("aria-expanded", "false");

    // Act
    await open.click();

    // Assert
    await expect(open).toHaveAttribute("aria-expanded", "true");
    const box = async (selector: string) => (await hank.locator(selector).first().boundingBox())!;
    const [row, name, time, fold, text] = await Promise.all([box(":scope"), box(".history-head strong"), box("time"), box(".history-open"), box(".inbox-summary")]);
    expect(Math.abs(name.y + name.height / 2 - (time.y + time.height / 2)), "the name and time share the header line").toBeLessThan(8);
    expect(Math.abs(fold.y + fold.height / 2 - (time.y + time.height / 2)), "the fold button stays on the header line").toBeLessThan(8);
    expect(text.y, "the whole text starts below the header line").toBeGreaterThanOrEqual(fold.y + fold.height - 4);
    expect(text.width, "the whole text runs the row's full width").toBeGreaterThan(row.width - 40);
    await expect(hank.locator(".inbox-summary")).toHaveText("Add the DNS records in Cloudflare for the docs site, then tell me");
    expect(await hank.locator(".inbox-summary").evaluate((element) => getComputedStyle(element).whiteSpace)).toBe("pre-wrap");
    const detail = hank.locator(".history-detail");
    await expect(detail).toBeVisible();
    await expect(detail).toContainText("You wrote: Added both records, the TXT one took a minute to show");
    await expect(rows.locator(".history-detail:visible")).toHaveCount(1);
    expect(await panel.evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);
    const reason = rows.filter({ hasText: "May PR 2's gate start now" });
    await reason.locator(".history-open").click();
    await expect(reason.locator(".history-detail")).toContainText("Reason: The running test step ends in 4 minutes");
    await panel.screenshot({ path: testInfo.outputPath(`history-${width}-open.png`) });

    await open.click();
    await expect(open).toHaveAttribute("aria-expanded", "false");
    await expect(detail).toBeHidden();
  });
}
