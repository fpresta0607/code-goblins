import { expect, test, type Page } from "./site";

const PACKET = "https://github.com/o/r/pull/209";
const PROMPT = "Repository o/r. Read the goblins handoff packet in the body of " + PACKET + " and follow its rules: branch from the head it names, push only to your own branch and open your pull request into the branch it names.";

// Records what the board posts to /api/tasks/lifecycle, with its token.
async function recordLifecycle(page: Page) {
  const sent: { body: unknown; token: string | null }[] = [];
  await page.route("**/api/tasks/lifecycle", async (route) => {
    sent.push({ body: route.request().postDataJSON(), token: await route.request().headerValue("X-CEO-Token") });
    await route.fulfill({ status: 202, json: { accepted: true, revision: 2 } });
  });
  return sent;
}

// A working goblin's controls offer Hand off, which asks the supervisor for
// a handoff of its session with the board's token.
test("Hand off on a working goblin asks for its handoff", async ({ page }) => {
  // Arrange
  const sent = await recordLifecycle(page);
  await page.goto("/tests/fixtures/cloud-handoff.html");

  // Act
  await page.getByRole("button", { name: "Hand off Parse headers" }).click();

  // Assert
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0]).toEqual({ body: { task: "cg-parser", generation: "s1", revision: "", action: "handoff", operation: expect.any(String) }, token: "fixture" });
});

// A handed-off goblin reads In the cloud, offers Resume and no Hand off or
// Pause, links the pull request its packet is in, and copies the prompt a
// cloud session is given; its Resume asks for a resume, which the supervisor
// runs as goblins handoff cancel.
test("a handed-off goblin reads In the cloud and copies its prompt", async ({ page }) => {
  // Arrange
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", { value: { writeText: (text: string) => { document.body.dataset.copied = text; return Promise.resolve(); } } });
  });
  const sent = await recordLifecycle(page);
  await page.goto("/tests/fixtures/cloud-handoff.html?task=cloud");
  const section = page.getByRole("region", { name: "In the cloud" });

  // Act
  await section.getByRole("button", { name: "Copy prompt" }).click();
  await page.getByRole("button", { name: "Resume Parse headers" }).click();

  // Assert
  await expect(page.locator(".task-card")).toContainText("In the cloud");
  await expect(page.getByRole("button", { name: "Hand off Parse headers" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Pause Parse headers" })).toHaveCount(0);
  await expect(section.getByRole("link", { name: "r #209" })).toHaveAttribute("href", PACKET);
  await expect(section).toContainText(PROMPT);
  await expect(section.getByRole("button", { name: "Copied" })).toBeVisible();
  await expect(page.locator("body")).toHaveAttribute("data-copied", PROMPT);
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ task: "cg-parser", generation: "s1", action: "resume" });
});

// At a phone's width the section and its button stay on the screen, with no
// page scroll sideways.
test("In the cloud fits a phone's width", async ({ page }) => {
  // Arrange
  await page.setViewportSize({ width: 390, height: 844 });

  // Act
  await page.goto("/tests/fixtures/cloud-handoff.html?task=cloud");

  // Assert
  await expect(page.getByRole("button", { name: "Copy prompt" })).toBeInViewport();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});
