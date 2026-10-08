import { fileURLToPath } from "node:url";
import { expect, ORIGIN, test } from "./site";

const CONTENT_SECURITY_POLICY = "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-placeholder-test'; style-src-attr 'unsafe-inline'; img-src 'self' data: https://avatars.githubusercontent.com; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'";

test("the unbuilt board placeholder is centered and legible under the supervisor's policy", async ({ page }) => {
  const policyViolations: string[] = [];
  page.on("console", (message) => {
    if (message.text().startsWith("CSP violation:")) policyViolations.push(message.text());
  });
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (event) => {
      console.error(`CSP violation: ${event.effectiveDirective} ${event.blockedURI}`);
    });
  });
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.route(ORIGIN + "/", (route) => route.fulfill({
    path: fileURLToPath(new URL("../../internal/boardweb/dist/index.html", import.meta.url)),
    contentType: "text/html; charset=utf-8",
    headers: { "Content-Security-Policy": CONTENT_SECURITY_POLICY },
  }));

  await page.goto("/");

  await expect(page.locator("body")).toHaveCSS("background-color", "rgb(3, 5, 10)");
  await expect(page.locator("body")).toHaveCSS("color", "rgb(237, 244, 243)");
  const heading = page.getByRole("heading", { name: "The board was not built", level: 1 });
  await expect(heading).toBeVisible();
  await expect(heading).toHaveCSS("color", "rgb(0, 229, 155)");
  const centerOffset = await page.getByRole("main").evaluate((card) => {
    const bounds = card.getBoundingClientRect();
    return {
      horizontal: Math.abs(bounds.x + bounds.width / 2 - innerWidth / 2),
      vertical: Math.abs(bounds.y + bounds.height / 2 - innerHeight / 2),
    };
  });
  expect(centerOffset.horizontal).toBeLessThanOrEqual(2);
  expect(centerOffset.vertical).toBeLessThanOrEqual(2);
  await expect(page.locator("style")).toHaveCount(0);
  expect(policyViolations).toEqual([]);
});
