import { expect, test } from "./site";

// The Overlord, 2026-10-08: "everything error wise goes to cfo". A card or a
// list that could not be drawn tells the CFO why, and leaves only a Retry in
// its place.
for (const mode of ["ranked", "history", "queued"]) {
  test(`${mode} rendering failures stay contained, tell the CFO, and can be retried`, async ({ page }) => {
    const scope = mode === "queued" ? "list" : "card";
    const retry = { name: `Show this ${scope} again` };
    const reports: { token: string | null; where: string; text: string }[] = [];
    await page.route("**/api/cfo/report", async (route) => {
      reports.push({ token: await route.request().headerValue("X-CFO-Token"), ...route.request().postDataJSON() });
      await route.fulfill({ status: 202, json: { reported: true } });
    });
    await page.goto(`/tests/fixtures/render-errors.html#${mode}`);
    const affected = page.getByRole("region", { name: "Affected list", exact: true });
    await expect(affected.getByRole("button", retry)).toBeVisible();
    await expect(affected.getByRole("alert")).toHaveCount(0);
    await expect(affected).not.toContainText("could not be shown");
    await expect.poll(() => reports[0]).toMatchObject({ token: "fixture", where: `drawing a ${scope} on the board`, text: expect.stringContaining(mode === "queued" ? "Fixture list failed to render" : "Fixture card failed to render") });
    await expect(page.getByRole("region", { name: "Unaffected list" })).toBeVisible();
    if (mode !== "queued") await expect(affected.getByRole("button", { name: "Healthy neighbor" })).toBeVisible();
    await affected.getByRole("button", retry).click();
    await expect(affected.getByRole("button", retry)).toBeVisible();
    await page.getByRole("button", { name: "Repair fixture" }).click();
    await expect(affected.getByRole("button", retry)).toBeVisible();
    await affected.getByRole("button", retry).click();
    await expect(affected.getByRole("button", retry)).toHaveCount(0);
    await expect(affected.getByText("Recovered card", { exact: true })).toBeVisible();
  });
}
