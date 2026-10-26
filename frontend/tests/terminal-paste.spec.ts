import { expect, test, type Page } from "./site";
import { card, NAME, open, pasted, typed } from "./session-board";

// The Overlord, 2026-10-26: "alt + v or ctrl + c both should work for paste".
// In a terminal, Ctrl+V is the browser's own paste; Alt+V starts none, so the
// board reads the clipboard itself. Ctrl+C stays the harness's interrupt.

const PICTURE = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

async function classicTerminal(page: Page) {
  await page.addInitScript(() => localStorage.setItem("cfo-chat-style", "classic"));
  const relay = await open(page);
  await card(page, NAME).click();
  const terminal = page.locator(".native-terminal:visible");
  await expect(terminal).toBeVisible();
  await expect(terminal.locator("textarea.xterm-helper-textarea")).toHaveCount(1);
  return { relay, terminal };
}

async function clipboard(page: Page, text: string, picture = "") {
  await page.addInitScript(({ words, image }) => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { read: async () => {
      if (!image) return [{ types: ["text/plain"], getType: async () => new Blob([words], { type: "text/plain" }) }];
      const bytes = Uint8Array.from(atob(image), (letter) => letter.charCodeAt(0));
      return [{ types: ["image/png"], getType: async () => new File([bytes], "shot.png", { type: "image/png" }) }];
    } } });
  }, { words: text, image: picture });
}

test("Alt+V pastes the clipboard's text into the terminal", async ({ page }) => {
  // Arrange
  await clipboard(page, "pasted into the terminal");
  const { relay, terminal } = await classicTerminal(page);

  // Act
  await terminal.locator("textarea.xterm-helper-textarea").focus();
  await page.keyboard.press("Alt+v");

  // Assert
  await expect.poll(() => typed(relay)).toContain(pasted("pasted into the terminal"));
});

test("Alt+V puts a clipboard picture on this PC's clipboard and presses the harness's image key", async ({ page }) => {
  // Arrange
  await clipboard(page, "", PICTURE);
  const { relay, terminal } = await classicTerminal(page);
  const posts: string[] = [];
  await page.route("**/api/terminal/image", (route) => {
    posts.push(route.request().headers()["content-type"] ?? "");
    return route.fulfill({ status: 201, json: {} });
  });

  // Act
  await terminal.locator("textarea.xterm-helper-textarea").focus();
  await page.keyboard.press("Alt+v");

  // Assert
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toBe("image/png");
  await expect.poll(() => typed(relay)).toContain("\x1bv");
});
