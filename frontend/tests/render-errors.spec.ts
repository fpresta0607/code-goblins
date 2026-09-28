import { expect, test } from "@playwright/test";

for (const mode of ["ranked", "history", "queued"]) {
  test(`${mode} rendering failures stay contained and can be retried`, async ({ page }) => {
    await page.goto(`/tests/fixtures/render-errors.html#${mode}`);
    const affected = page.getByRole("region", { name: "Affected list" });
    await expect(affected.getByRole("alert")).toHaveText(`This ${mode === "queued" ? "list" : "card"} could not be shown.`);
    await expect(page.getByRole("region", { name: "Unaffected list" })).toBeVisible();
    if (mode !== "queued") await expect(affected.getByRole("button", { name: "Healthy neighbor" })).toBeVisible();
    await affected.getByRole("button", { name: "Retry" }).click();
    await expect(affected.getByRole("alert")).toBeVisible();
    await page.getByRole("button", { name: "Repair fixture" }).click();
    await expect(affected.getByRole("alert")).toBeVisible();
    await affected.getByRole("button", { name: "Retry" }).click();
    await expect(affected.getByRole("alert")).toHaveCount(0);
    await expect(affected.getByText("Recovered card", { exact: true })).toBeVisible();
  });
}
