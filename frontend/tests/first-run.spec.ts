import { expect, test, type Page } from "@playwright/test";

// The Overlord, 2026-10-02, after the board's first-run page started his CFO
// in a project: "the default repo should always be code goblins; it should
// not be a random project repo, so that shouldn't even be an option on the
// start selection", "shouldn't it adopt the answers from the CLI", and
// "shouldn't it be a tab or a drop-down switch or icon selection for the
// harness?" The page asks for no project, shows as done what the quick start
// already knows, and offers the agents as one row of icon tabs.
const missing = "Install pi to start the CFO";
const setup = (changes: Record<string, unknown> = {}) => ({
  home: "C:\\Users\\franco\\AppData\\Local\\CodeGoblins",
  agent: "claude",
  projects_root: "C:\\dev",
  checkouts: ["alpha", "beta"],
  agents: [
    { id: "claude", name: "Claude Code", recommended: true, note: "the best experience", installed: true, signed_in: true },
    { id: "codex", name: "Codex", recommended: false, note: "woken by a typed line; no digest or guards", installed: true, signed_in: true },
    { id: "pi", name: "pi", recommended: false, note: "woken by a typed line; no digest, guards or resume", installed: false, signed_in: false, reason: missing },
  ],
  cfo_runs: false,
  ...changes,
});

// open shows the page over a machine: every folder it looks at is answered
// by looked, and each Start is recorded with its body.
async function open(page: Page, machine: Record<string, unknown>, looked: (root: string) => Record<string, unknown> = () => machine) {
  const starts: unknown[] = [];
  await page.route("**/api/setup?root=*", (route) => {
    const root = new URL(route.request().url()).searchParams.get("root") ?? "";
    return route.fulfill({ json: root ? looked(root) : machine });
  });
  await page.route("**/api/setup/start", (route) => {
    starts.push(route.request().postDataJSON());
    return route.fulfill({ json: { started: true } });
  });
  await page.goto("/tests/fixtures/first-run.html");
  await expect(page.getByRole("heading", { name: "Start Code Goblins" })).toBeVisible();
  return starts;
}

test.use({ viewport: { width: 1280, height: 900 } });

test("the page asks for no project, shows what the quick start knows as done, and starts the CFO in its home", async ({ page }, testInfo) => {
  // Arrange
  const starts = await open(page, setup());
  const done = page.getByRole("list", { name: "Already set up" });

  // Assert: nothing offers a project to start in.
  await expect(page.getByText("Project the CFO starts in")).toHaveCount(0);
  await expect(page.getByRole("combobox")).toHaveCount(0);
  await expect(page.getByRole("radio")).toHaveCount(0);
  await expect(done.getByText("C:\\Users\\franco\\AppData\\Local\\CodeGoblins")).toBeVisible();
  await expect(done.getByText("Claude Code, remembered for this home")).toBeVisible();
  await expect(page.getByText("Goblins find 2 projects here by name: alpha, beta.")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("first-run.png") });

  // Act
  await page.getByRole("button", { name: "Start the CFO" }).click();

  // Assert: the folder he never looked at is not sent, and no project is.
  await expect(page.getByRole("status", { name: "Outcome" })).toHaveText("The CFO's terminal opens");
  expect(starts).toEqual([{ root: "", agent: "claude" }]);
});

test("the agents are one row of icon tabs moved with Left and Right", async ({ page }, testInfo) => {
  // Arrange
  await open(page, setup({ agent: "" }));
  const tabs = page.getByRole("tablist", { name: "Agent" });
  const claude = tabs.getByRole("tab", { name: /Claude Code/ });
  const codex = tabs.getByRole("tab", { name: "Codex" });
  const start = page.getByRole("button", { name: "Start the CFO" });

  // Assert: three tabs, each with its mark, Claude Code recommended and
  // marked when the quick start remembered none, and nothing said to be
  // remembered for this home.
  await expect(tabs.getByRole("tab")).toHaveCount(3);
  await expect(tabs.locator("svg")).toHaveCount(3);
  await expect(claude).toHaveAttribute("aria-selected", "true");
  await expect(claude).toContainText("Recommended");
  await expect(page.getByText("remembered for this home")).toHaveCount(0);
  await expect(page.getByRole("tabpanel")).toContainText("Signed in");
  await expect(start).toBeEnabled();

  // Act
  await claude.focus();
  await page.keyboard.press("ArrowRight");

  // Assert: the mark and the focus move, the page says what a Codex CFO
  // gets, and Codex starts like any agent the fleet can wake.
  await expect(codex).toHaveAttribute("aria-selected", "true");
  await expect(codex).toBeFocused();
  await expect(page.getByRole("tabpanel")).toContainText("woken by a typed line; no digest or guards");
  await expect(codex).not.toContainText("Recommended");
  await expect(start).toBeEnabled();
  await page.screenshot({ path: testInfo.outputPath("first-run-codex.png") });

  // Act: Left from the first tab wraps to the last.
  await page.keyboard.press("ArrowLeft");
  await page.keyboard.press("ArrowLeft");

  // Assert
  await expect(tabs.getByRole("tab", { name: "pi", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tabpanel")).toContainText("Not installed");
  await expect(page.getByRole("tabpanel")).toContainText(missing);
  await expect(start).toBeDisabled();
});

test("an agent the quick start remembered is shown as chosen and starts as it is, and another is one click away", async ({ page }) => {
  // Arrange
  const starts = await open(page, setup({ agent: "codex" }));
  const start = page.getByRole("button", { name: "Start the CFO" });

  // Assert
  await expect(page.getByRole("tab", { name: "Codex" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("list", { name: "Already set up" }).getByText("Codex, remembered for this home")).toBeVisible();
  await expect(start).toBeEnabled();

  // Act
  await start.click();

  // Assert
  await expect(page.getByRole("status", { name: "Outcome" })).toHaveText("The CFO's terminal opens");
  expect(starts).toEqual([{ root: "", agent: "codex" }]);
});

test("an agent this machine lacks says why and holds Start back until another is picked", async ({ page }) => {
  // Arrange
  const starts = await open(page, setup({ agent: "pi" }));
  const start = page.getByRole("button", { name: "Start the CFO" });

  // Assert
  await expect(page.getByRole("tab", { name: "pi", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("tabpanel")).toContainText(missing);
  await expect(start).toBeDisabled();

  // Act
  await page.getByRole("tab", { name: /Claude Code/ }).click();
  await start.click();

  // Assert
  await expect(page.getByRole("status", { name: "Outcome" })).toHaveText("The CFO's terminal opens");
  expect(starts).toEqual([{ root: "", agent: "claude" }]);
});

test("the projects folder is optional: one he looked at is sent, and one with no project holds Start back", async ({ page }) => {
  // Arrange
  const noCheckout = "No git checkout is in this folder; pick the folder that holds your projects.";
  const starts = await open(page, setup({ projects_root: "", checkouts: [] }), (root) => root === "C:\\work"
    ? setup({ projects_root: root, checkouts: ["gamma"] })
    : setup({ projects_root: root, checkouts: [], problem: noCheckout }));
  const folder = page.getByLabel("Projects folder");
  const start = page.getByRole("button", { name: "Start the CFO" });
  await expect(start).toBeEnabled();

  // Act: a folder with no checkout.
  await folder.fill("C:\\empty");
  await page.getByRole("button", { name: "Look" }).click();

  // Assert
  await expect(page.getByRole("alert")).toHaveText(noCheckout);
  await expect(start).toBeDisabled();

  // Act: a folder with one.
  await folder.fill("C:\\work");
  await page.getByRole("button", { name: "Look" }).click();
  await expect(page.getByText("Goblins find 1 project here by name: gamma.")).toBeVisible();
  await start.click();

  // Assert
  await expect(page.getByRole("status", { name: "Outcome" })).toHaveText("The CFO's terminal opens");
  expect(starts).toEqual([{ root: "C:\\work", agent: "claude" }]);
});

for (const [name, entered] of [["an empty field", ""], ["only spaces", "   "]]) {
  test(`a Look with ${name} is no folder: the recorded folder's problem does not hold Start back`, async ({ page }) => {
    // Arrange
    const unreadable = "This folder cannot be read: the folder was moved.";
    const starts = await open(page, setup({ projects_root: "C:\\gone", checkouts: [], problem: unreadable }));
    const start = page.getByRole("button", { name: "Start the CFO" });
    await expect(start).toBeEnabled();

    // Act
    await page.getByLabel("Projects folder").fill(entered);
    await page.getByRole("button", { name: "Look" }).click();

    // Assert
    await expect(page.getByRole("alert")).toHaveText(unreadable);
    await expect(start).toBeEnabled();
    await start.click();
    await expect(page.getByRole("status", { name: "Outcome" })).toHaveText("The CFO's terminal opens");
    expect(starts).toEqual([{ root: "", agent: "claude" }]);
  });
}
