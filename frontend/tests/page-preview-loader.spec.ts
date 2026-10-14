import { expect, test, ORIGIN, type Page } from "./site";

// The Overlord, 2026-10-14 about 22:40Z, of a Scrawl page's card in the
// Command Center: "Takes a few seconds for the preview to actually show ...
// make sure it has some type of animation, not just a blank screen." Shown a
// goblin for it, he answered "use a cool glimmer animation not the goblin".
// The frame glimmers until the page has loaded, and says so plainly when it
// cannot be shown.
const PREVIEW = ORIGIN + "/api/reviews/waiting-cg-loader-1/preview";
const PAGE = "<!doctype html><title>Plan</title><h1>The plan</h1>";

async function openCard(page: Page) {
  await page.goto("/tests/fixtures/page-preview-loader.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  return page.getByRole("dialog");
}

test("a glimmer shows in the frame while the page loads and gives way once it has loaded", async ({ page }) => {
  // Arrange
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  await page.route(PREVIEW, async (route) => { await held; await route.fulfill({ contentType: "text/html", body: PAGE }); });

  // Act
  const dialog = await openCard(page);

  // Assert
  const loading = dialog.locator(".page-preview").getByRole("status");
  await expect(loading).toHaveText("Loading the page");
  await expect(loading.locator(".page-bars")).toBeVisible();
  await expect(loading.locator(".page-sweep")).not.toHaveCSS("animation-name", "none");
  await expect(dialog.locator(".goblin-avatar").and(dialog.locator(".page-preview *"))).toHaveCount(0);
  release();
  await expect(dialog.locator(".page-preview").getByRole("status")).toHaveCount(0);
  await expect(dialog.locator(".page-preview iframe")).toHaveAttribute("src", "/api/reviews/waiting-cg-loader-1/preview");
});

test("a page that cannot be shown says so in two words instead of animating forever", async ({ page }) => {
  // Arrange
  await page.route(PREVIEW, (route) => route.fulfill({ status: 502, contentType: "application/json", body: '{"error":"The page cannot be shown"}' }));

  // Act
  const dialog = await openCard(page);

  // Assert
  await expect(dialog.locator(".page-preview").getByText("Page unavailable")).toBeVisible();
  await expect(dialog.getByText("Loading the page")).toHaveCount(0);
  await expect(dialog.locator(".page-preview iframe")).toHaveCount(0);
});

test("a page that never connects says so too", async ({ page }) => {
  // Arrange
  await page.route(PREVIEW, (route) => route.abort("connectionrefused"));

  // Act
  const dialog = await openCard(page);

  // Assert
  await expect(dialog.getByText("Page unavailable")).toBeVisible();
  await expect(dialog.locator(".page-preview iframe")).toHaveCount(0);
});

test("with reduced motion the glimmer stands still beside its line", async ({ page }) => {
  // Arrange
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.route(PREVIEW, () => new Promise(() => {}));

  // Act
  const dialog = await openCard(page);

  // Assert
  const loading = dialog.locator(".page-preview").getByRole("status");
  await expect(loading).toHaveText("Loading the page");
  await expect(loading.locator(".page-sweep")).toHaveCSS("animation-name", "none");
});

test("the glimmer and its line fit the frame on a phone", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route(PREVIEW, () => new Promise(() => {}));

  // Act
  const dialog = await openCard(page);

  // Assert
  const frame = await dialog.locator(".page-preview").boundingBox();
  const line = await dialog.getByText("Loading the page").boundingBox();
  const bars = await dialog.locator(".page-loading .page-bars").boundingBox();
  expect(frame && line && bars).toBeTruthy();
  for (const box of [line!, bars!]) {
    expect(box.x).toBeGreaterThanOrEqual(frame!.x);
    expect(box.x + box.width).toBeLessThanOrEqual(frame!.x + frame!.width);
    expect(box.y).toBeGreaterThanOrEqual(frame!.y);
    expect(box.y + box.height).toBeLessThanOrEqual(frame!.y + frame!.height);
  }
});
