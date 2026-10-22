import { expect, test, type Page } from "./site";

// The Overlord, 2026-10-17, of Kiki's IPRoyal refund card, whose steps named
// the file holding the email: "With those steps to file, you should have a
// little like copy email body or copy email that I could then send. Instead
// of having to go to like this file. And if there is a folder, there should
// be a link to the open the file explorer folder on my PC just to make
// things easier." A card's message shows with Copy, and each local
// path a card names is a link: File Explorer at its folder from the desktop
// window, a copy of the path in a plain browser.

const EMAIL = { subject: "Refund for order 4412", body: "Hello,\n\nPlease refund order 4412: the proxies never connected.\n\nThanks,\nNick" };
const FORM_BODY = "Please refund order 4412.\nThe proxies never connected.";
const CARDS = [
  { open: "Open the step card", kind: "credential", id: "cred-5555555555555555", body: EMAIL.body, subject: EMAIL.subject, path: String.raw`C:\dev\code-goblins\data\pd-proxy-primary\iproyal-refund.md` },
  { open: "Open the question", kind: "question", id: "q-proxy-notes", body: FORM_BODY, subject: "", path: String.raw`C:\Users\fpres\OneDrive - SIQstack\Desktop` },
  { open: "Open the command", kind: "run", id: "run-pd-proxy-primary-9", body: EMAIL.body, subject: EMAIL.subject, path: String.raw`C:\work\pd-proxy` },
] as const;

async function openCard(page: Page, name: string) {
  await page.goto("/tests/fixtures/card-mail.html");
  await page.getByRole("button", { name, exact: true }).click();
  const card = page.getByRole("dialog").locator(".question-card, .run-card").first();
  await expect(card).toBeVisible();
  return card;
}

// Windows keeps a copied line break as a carriage return and a line feed.
const clipboard = async (page: Page) => (await page.evaluate(() => navigator.clipboard.readText())).replaceAll("\r\n", "\n");

for (const c of CARDS) {
  test(`Copy on the ${c.kind} card puts its exact body on the clipboard`, async ({ page, context }) => {
    // Arrange
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    const card = await openCard(page, c.open);
    const mail = card.getByRole("region", { name: "Message to send" });

    // Act
    await mail.getByRole("button", { name: "Copy", exact: true }).click();

    // Assert
    await expect(mail.getByRole("button", { name: "Copied" })).toBeVisible();
    expect(await clipboard(page)).toBe(c.body);
    await expect(mail.getByLabel("Message body")).toHaveText(c.body);
    await expect(mail.getByRole("button", { name: "Copy subject" })).toHaveCount(c.subject ? 1 : 0);
  });

  test(`in the desktop window a path the ${c.kind} card names asks the supervisor to open File Explorer at it`, async ({ page }) => {
    // Arrange: WebView2 puts chrome.webview in the desktop window's page.
    await page.addInitScript(() => { (window as unknown as { chrome: Record<string, unknown> }).chrome = { ...(window as unknown as { chrome?: Record<string, unknown> }).chrome, webview: {} }; });
    const sent: { token: string | null; body: unknown }[] = [];
    await page.route("**/api/cards/open-path", async (route) => {
      sent.push({ token: await route.request().headerValue("X-CFO-Token"), body: route.request().postDataJSON() });
      await route.fulfill({ status: 200, json: { message: "Opened File Explorer." } });
    });
    const card = await openCard(page, c.open);
    const link = card.getByRole("button", { name: "Open " + c.path + " in File Explorer" });
    await expect(link).toHaveAttribute("data-tip", "Open in File Explorer");

    // Act
    await link.click();

    // Assert
    await expect.poll(() => sent).toEqual([{ token: "fixture-token", body: { kind: c.kind, id: c.id, path: c.path } }]);
    await expect(link).toHaveAttribute("data-tip", "Opened in File Explorer");
  });
}

test("in a plain browser a path link copies the path, says so in its tip, and never asks the supervisor", async ({ page, context }) => {
  // Arrange
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const asked: string[] = [];
  await page.route("**/api/cards/open-path", async (route) => {
    asked.push(route.request().url());
    await route.fulfill({ status: 500, json: { error: "a plain browser asked to open a folder" } });
  });
  const step = CARDS[0];
  const card = await openCard(page, step.open);
  const link = card.getByRole("button", { name: "Copy the path " + step.path });
  await expect(link).toHaveAttribute("data-tip", "Copy the path: File Explorer opens it only from the desktop window");

  // Act
  await link.click();

  // Assert
  await expect(link).toHaveAttribute("data-tip", "Copied");
  expect(await clipboard(page)).toBe(step.path);
  await expect(card.getByRole("list", { name: "What to do" }).getByRole("listitem").first()).toHaveText("Read the refund notes in " + step.path + ".");
  expect(asked).toEqual([]);
});

// A wait's one line is its card's heading, and a path it names is a link
// there too, as it is in the rest of a card's words.
test("in the desktop window a path a wait names in its one line opens File Explorer", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => { (window as unknown as { chrome: Record<string, unknown> }).chrome = { ...(window as unknown as { chrome?: Record<string, unknown> }).chrome, webview: {} }; });
  const sent: unknown[] = [];
  await page.route("**/api/cards/open-path", async (route) => {
    sent.push(route.request().postDataJSON());
    await route.fulfill({ status: 200, json: { message: "Opened File Explorer." } });
  });
  await page.goto("/tests/fixtures/card-mail.html");
  await page.getByRole("button", { name: "Open the wait", exact: true }).click();
  const heading = page.getByRole("dialog").getByRole("heading", { level: 3 });

  // Act
  await heading.getByRole("button", { name: "Open " + CARDS[0].path + " in File Explorer" }).click();

  // Assert
  await expect.poll(() => sent).toEqual([{ kind: "review", id: "waiting-pd-proxy-primary-7", path: CARDS[0].path }]);
});

// A step's code value that is a path, spaces and all, keeps its button that
// copies it, and its folder icon opens the whole path, never the part before
// its first space.
test("a step's code value that is a path keeps its copy button and its icon opens the whole path", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => { (window as unknown as { chrome: Record<string, unknown> }).chrome = { ...(window as unknown as { chrome?: Record<string, unknown> }).chrome, webview: {} }; });
  const sent: unknown[] = [];
  await page.route("**/api/cards/open-path", async (route) => {
    sent.push(route.request().postDataJSON());
    await route.fulfill({ status: 200, json: { message: "Opened File Explorer." } });
  });
  const desktop = CARDS[1].path;
  const card = await openCard(page, CARDS[0].open);
  const step = card.getByRole("list", { name: "What to do" }).getByRole("listitem").last();

  // Act
  await step.getByRole("button", { name: "Open " + desktop + " in File Explorer" }).click();

  // Assert
  await expect(step.getByRole("button", { name: "Copy " + desktop })).toBeVisible();
  await expect(step.getByRole("button", { name: /^Open .* in File Explorer$/ })).toHaveCount(1);
  await expect.poll(() => sent).toEqual([{ kind: "credential", id: CARDS[0].id, path: desktop }]);
});
