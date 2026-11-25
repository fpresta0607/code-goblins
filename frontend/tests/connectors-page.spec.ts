import { fleet, NOW, play, type Supervisor } from "./connectors-supervisor";
import { expect, holdStream, openTaskList, taskList, test, type Locator, type Page } from "./site";

// The Connectors page as a drill-down (#2902): This PC and its accounts, then
// each project, then each goblin, each row saying which level it is of, and a
// search over every level. One page in the board, reached from its top bar,
// whose supervisor is played here.
const goblin = (id: string, name: string, title: string, project: string) => ({
  id, title: "A made-up task of " + name, goblin_name: name, goblin_title: title, project, phase: "working", verified: false,
  generation: id + "-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", since: "2026-11-10T09:00:00Z",
});
const BOARD = {
  instance: "fixture", healthy: true, revision: 1, ceo_runs: true, ceo_terminal: "ceo", ceo_terminal_since: "2026-11-10T09:00:00Z", ceo_harness: "claude",
  sessions: [{ id: "ceo-1", harness: "claude", role: "ceo", model: "claude-opus-5-5" }],
  tasks: [goblin("cg-touch", "Zeke", "Touch Wrangler", "acme"), goblin("cg-other", "Ivy", "Window Painter", "globex")],
};
const ZEKE = "Zeke - Touch Wrangler";

// The board, with the Connectors page chosen from its top bar.
async function open(page: Page, supervisor: Supervisor, width: number): Promise<Locator> {
  await page.setViewportSize({ width, height: 1100 });
  await page.clock.install({ time: Date.parse(NOW) });
  await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
  await holdStream(page, BOARD);
  await play(page, supervisor);
  await page.goto("/");
  await page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Connectors", exact: true }).click();
  const main = page.getByRole("main", { name: "Connectors" });
  await expect(main.getByRole("heading", { name: "Connectors", exact: true })).toBeVisible();
  await expect(main.getByRole("region", { name: "Connectors view" })).toBeVisible();
  return main;
}

const group = (main: Locator, title: string) => main.getByRole("region", { name: title, exact: true });
const crumbs = (main: Locator) => main.getByRole("navigation", { name: "Where you are" });

// Nothing on the page is wider than its place, and none of its words is small.
async function expectFits(page: Page, main: Locator) {
  await page.evaluate(() => document.fonts.ready);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "the page does not scroll sideways").toBe(true);
  expect(await main.evaluate((element) => element.scrollWidth <= element.clientWidth), "the canvas does not scroll sideways").toBe(true);
  const small = await main.locator(".connectors-page").evaluate((element) => [...element.querySelectorAll("h2, h3, p, span, strong, button, input, summary, li, small, b")]
    .filter((node) => node.textContent?.trim() && parseFloat(getComputedStyle(node).fontSize) < 16).map((node) => node.textContent));
  expect(small).toEqual([]);
}

for (const width of [1440, 390]) {
  test(`the page drills down from this PC to a project to a goblin and back up by its breadcrumb, each row saying its level, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    const supervisor = fleet();
    const main = await open(page, supervisor, width);

    // Assert: home holds a tile for each level and what needs a look from every level.
    await expect(main.locator(".connector-tile strong")).toHaveText(["This PC and its accounts", "2 projects", "2 goblins"]);
    const attention = group(main, "Needs a look");
    await expect(attention.locator(".connector-open")).toHaveText(["gcloud", "ACME_API_KEY", "Supabase", "Acme Docs"]);
    await expect(attention.locator(".connector-level")).toHaveText(["This PC", "This PC", "Project", "Goblin"]);
    await expect(attention.locator(".connector-state b")).toHaveText(["Not checked", "Every goblin has it", "Missing", "Failed to load"]);
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-home-${width}.png`) });

    // Act: this PC and its accounts.
    await main.getByRole("button", { name: /^Level 1/ }).click();

    // Assert: its groups, each row of the PC or the Account level.
    await expect(crumbs(main).locator(".here")).toHaveText("This PC and its accounts");
    await expect(main.locator(".connector-group > h3")).toHaveText(["CLI tools3", "This PC's environment1", "Built in to every goblin1", "Harness accounts1", "Comes with your Claude account1"]);
    await expect(group(main, "CLI tools").locator(".connector-name strong")).toHaveText(["gh", "vercel", "gcloud"]);
    await expect(group(main, "CLI tools").locator(".connector-state b")).toHaveText(["Verified", "Installed", "Not checked"]);
    await expect(group(main, "Harness accounts").locator(".connector-level")).toHaveText("Account");
    await expect(group(main, "Comes with your Claude account").locator(".connector-state b")).toHaveText("Not given");
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-pc-${width}.png`) });

    // Act: back to the home by the breadcrumb, then the projects.
    await crumbs(main).getByRole("button", { name: "Connectors", exact: true }).click();
    await expect(main.getByRole("button", { name: /^Level 1/ })).toBeFocused();
    await main.getByRole("button", { name: /^Level 2/ }).click();

    // Assert: each project once, the busiest first, with how much it holds.
    await expect(group(main, "Projects").locator(".connector-open")).toHaveText(["acme", "globex"]);
    await expect(group(main, "Projects").locator(".connector-sub")).toHaveText(["2 services · 1 MCP server · 1 goblin", "1 service · 1 goblin"]);
    await expect(group(main, "Projects").locator(".connector-stale")).toHaveCount(1);

    // Act: a project.
    await main.getByRole("button", { name: "acme", exact: true }).click();

    // Assert: its services with their credential names and what each points at, its MCP servers and its goblins.
    await expect(crumbs(main).locator(".here")).toHaveText("acme");
    const services = group(main, "Services");
    await expect(services.locator(".connector-name strong")).toHaveText(["GitHub", "Supabase"]);
    await expect(services.locator(".connector-state b")).toHaveText(["Verified", "Set"]);
    await expect(services.locator(".connector-sub").nth(1)).toContainText("Supabase project …k3tv");
    await expect(services.getByRole("list", { name: "Credential names of Supabase" }).locator("li")).toHaveText(["SUPABASE_URLset", "SUPABASE_SERVICE_KEYnot set"]);
    await expect(group(main, "MCP servers").locator(".connector-state b")).toHaveText("Not given");
    await expect(group(main, "Goblins").locator(".connector-open")).toHaveText(ZEKE);
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-project-${width}.png`) });

    // Act: a goblin.
    await main.getByRole("button", { name: ZEKE, exact: true }).click();

    // Assert: it sits under its project, and shows only what differs.
    await expect(crumbs(main).locator("button, .here")).toHaveText(["Connectors", "Projects", "acme", ZEKE]);
    await expect(group(main, "Failed to load").locator(".connector-state")).toContainText("Connection closed");
    await expect(group(main, "Loaded by its session").locator(".connector-state b")).toHaveText("Loaded");
    await expect(group(main, "Loaded by its session").locator("summary")).toHaveText("Tools2");
    await expect(group(main, "Carries").getByRole("button", { name: "Disconnect GitHub from " + ZEKE })).toBeVisible();
    await expect(group(main, "Not given").getByRole("button", { name: "Connect Supabase to " + ZEKE })).toBeVisible();
    await expect(main.locator(".connector-row .connector-refresh")).toHaveCount(0);
    await expect(main.locator("details.connector-same")).not.toHaveAttribute("open", "");
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-goblin-${width}.png`) });

    // Act: back up by the breadcrumb, a step at a time.
    await crumbs(main).getByRole("button", { name: "acme", exact: true }).click();

    // Assert: the way back lands on the row it came down by.
    await expect(crumbs(main).locator(".here")).toHaveText("acme");
    await expect(main.getByRole("button", { name: ZEKE, exact: true })).toBeFocused();

    // Act
    await crumbs(main).getByRole("button", { name: "Projects", exact: true }).click();

    // Assert
    await expect(main.getByRole("button", { name: "acme", exact: true })).toBeFocused();
    expect(supervisor.posts).toEqual([]);
  });

  test(`a search finds items at every level, grouped by level, same-named items of two projects apart with their project and target, and each result opens its place, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    const supervisor = fleet();
    const main = await open(page, supervisor, width);
    const search = main.getByRole("searchbox", { name: "Search connectors" });
    const readsBefore = supervisor.reads;

    // Act
    await search.fill("supabase");

    // Assert: the account's connector, and each project's service and server, under their levels.
    await expect(crumbs(main).locator(".here")).toHaveText("Search: every level");
    await expect(main.locator(".connector-group > h3")).toHaveText(["Account1", "Project3", "Goblin1"]);
    const projects = group(main, "Project");
    await expect(projects.locator(".connector-sub")).toHaveText(["acme · Supabase project …k3tv", "acme · Supabase project …k3tv", "globex · Supabase project …q7wk"]);
    await expect(projects.getByRole("button", { name: "Supabase, globex" })).toBeVisible();
    await expect(main.getByText("5 matches, searched in the saved snapshot: nothing was checked to answer it.")).toBeVisible();
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-search-${width}.png`) });

    // Act: a word that every level holds.
    await search.fill("acme");

    // Assert
    await expect(main.locator(".connector-group > h3")).toHaveText(["This PC1", "Project3", "Goblin4"]);

    // Act: a result opens its place, and the search ends.
    await projects.getByRole("button", { name: "Supabase, acme" }).first().click();

    // Assert
    await expect(search).toHaveValue("");
    await expect(crumbs(main).locator(".here")).toHaveText("acme");

    // Act: words nothing holds.
    await search.fill("zzz");

    // Assert
    await expect(main.getByText("Nothing matches \"zzz\".", { exact: true })).toBeVisible();
    expect(supervisor.reads).toBe(readsBefore);
    expect(supervisor.posts).toEqual([]);
  });
}

for (const width of [1440, 390]) {
  test(`a goblin's task panel holds no connectors panel, and its Connectors line opens the page, at ${width}px`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 1100 });
    await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
    await holdStream(page, BOARD);
    await play(page, fleet());
    await page.goto("/");

    // Act
    await page.locator(".task-card").filter({ hasText: "Zeke" }).first().click();
    await openTaskList(page);

    // Assert
    await expect(taskList(page).locator(".panel-content > details.disclosure > summary")).toHaveText(["Workspace", "Harness", "Changes", "Activity", "History"]);
    await expect(page.locator("details.connections, .connections-panel, .connector-card")).toHaveCount(0);
    await expect(page.getByRole("region", { name: "Connections" })).toHaveCount(0);

    // Act
    await taskList(page).getByRole("button", { name: "Connectors", exact: true }).click();

    // Assert: the page shows, beside the panel or in its place on a phone.
    const main = page.getByRole("main", { name: "Connectors" });
    await expect(main.locator(".connector-tile").first()).toBeVisible();
    await expect(page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Connectors", exact: true })).toHaveAttribute("aria-pressed", "true");
    if (width === 1440) await expect(page.locator("aside.context-pane")).toBeVisible();
    else await expect(page.locator("aside.context-pane")).toBeHidden();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

    // Act: a panel closed beside the page leaves the button that brings it back.
    if (width === 1440) {
      await page.getByRole("button", { name: "Back to the CEO" }).click();
      await page.getByRole("button", { name: "Close panel" }).click();
      await expect(page.locator("aside.context-pane")).toBeHidden();
      await page.getByRole("button", { name: "Open CEO", exact: true }).click();

      // Assert
      await expect(page.locator("aside.context-pane")).toBeVisible();
      await expect(main.locator(".connector-tile").first()).toBeVisible();
    }
  });
}

// The top bar's switch gained its third view here, and at 320px it was wider
// than the bar, so the whole board scrolled sideways.
for (const width of [320, 360]) {
  test(`the top bar's three views fit a ${width}px screen, each name whole`, async ({ page }) => {
    // Arrange
    await page.setViewportSize({ width, height: 800 });
    await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
    await holdStream(page, BOARD);
    await play(page, fleet());

    // Act
    await page.goto("/");
    const views = page.getByRole("group", { name: "Workspace view" });
    await expect(views.getByRole("button")).toHaveText(["Board", "Orchestration", "Connectors"]);
    await page.evaluate(() => document.fonts.ready);

    // Assert
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "the board does not scroll sideways").toBe(true);
    const fit = await views.evaluate((group) => {
      const bar = group.closest(".topbar")!.getBoundingClientRect(), box = group.getBoundingClientRect();
      const cut = [...group.querySelectorAll("button")].filter((button) => {
        const words = document.createRange();
        words.selectNodeContents(button);
        return words.getBoundingClientRect().width > button.clientWidth + 0.5 || parseFloat(getComputedStyle(button).fontSize) < 15;
      }).map((button) => button.textContent);
      return { isInsideItsBar: box.left >= bar.left && box.right <= bar.right, cut };
    });
    expect(fit).toEqual({ isInsideItsBar: true, cut: [] });
  });
}

test("with no goblin running the page still shows this PC and every project from the saved snapshot", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.view.goblins = [];
  await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
  await holdStream(page, { ...BOARD, tasks: [{ ...goblin("cg-queued", "Quill", "Waiting", "acme"), generation: "", phase: "queued" }] });
  await play(page, supervisor);
  await page.goto("/");

  // Act
  await page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Connectors", exact: true }).click();
  const main = page.getByRole("main", { name: "Connectors" });
  await main.getByRole("button", { name: /^Level 2/ }).click();
  await main.getByRole("button", { name: "acme", exact: true }).click();

  // Assert
  await expect(main.getByText("No goblin is running in this project.", { exact: true })).toBeVisible();
  await expect(main.getByRole("region", { name: "Services" })).toBeVisible();
});
