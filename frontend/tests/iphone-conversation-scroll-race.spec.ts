import { devices, servePages, test } from "./site";
import { delayedScroll } from "./conversation-scroll-race";

const PRIVATE = "https://pc.example.ts.net:4311";
test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE });

for (const update of ["stream", "board"] as const) {
  test(`WebKit keeps its place before scroll delivery during a ${update} update`, async ({ page }) => {
    await servePages(page.context(), PRIVATE);
    await delayedScroll(page, "goblin", "wheel", update);
  });
}
