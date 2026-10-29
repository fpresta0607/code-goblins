import { devices, expect, servePages, test, type Page } from "./site";
import { open } from "./session-board";

// The Overlord, 2026-10-29 about 11:24Z, from his phone, of the CFO's chat in
// Classic with a row of its own for the chat button and Back stacked over the
// CFO's header: "I don't like how it's stacked to have back and then the chat
// button. Especially in classic mode. The back button should definitely be
// slotted in somewhere into the classic and modern view to get back to the
// home screen. You could even just click on the CFO in the top, the CFO
// avatar, and that should take us back to the home screen." His phone is an
// iPhone, so this runs in WebKit, at the private board's address, since
// WebKit refuses the suite's own port.
const PRIVATE = "https://pc.example.ts.net:4311";
test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE });

const pane = (page: Page) => page.locator(".context-pane");
const header = (page: Page) => page.locator(".goblin-panel > .panel-header");

async function openCfo(page: Page, style: "classic" | "modern") {
  await page.addInitScript((chosen) => localStorage.setItem("cfo-chat-style", chosen), style);
  await servePages(page.context(), PRIVATE);
  await open(page);
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().tap();
  await expect(header(page).locator("#panel-title")).toHaveText("CFO");
  if (style === "classic") await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
  else await expect(page.getByRole("region", { name: "Messages", exact: true })).toBeVisible();
}

for (const style of ["classic", "modern"] as const) {
  for (const way of ["Back to Board", "Home"]) {
    test(`on an iPhone the CFO's ${style} panel has nothing above its header, and ${way} in it goes back to the board`, async ({ page }, testInfo) => {
      // Arrange
      await openCfo(page, style);
      const button = header(page).getByRole("button", { name: way, exact: true });
      if (way === "Home") await expect(button.locator(".goblin-avatar")).toBeVisible();

      // Assert: the header is the panel's top, with its way back in it.
      const paneBox = (await pane(page).boundingBox())!;
      const headerBox = (await header(page).boundingBox())!;
      await testInfo.attach(`cfo-${style}.png`, { body: await page.screenshot(), contentType: "image/png" });
      expect(headerBox.y - paneBox.y, "the header's distance from the panel's top").toBeLessThanOrEqual(1);
      await expect(button).toBeInViewport();

      // Act
      await button.tap();

      // Assert
      await expect(pane(page)).toBeHidden();
      await expect(page.locator(".task-board")).toBeVisible();
    });
  }
}
