import { fleet, NOW, play, type Supervisor } from "./connectors-supervisor";
import { expect, holdStream, openTaskList, taskList, test, type Locator, type Page } from "./site";

// The Connectors page as a tree of where connectors live (#3010, after the
// drill-down of #2902): This PC, then each project with its goblins under it,
// then your own Claude and Codex, each row tagged with its scope, and a search
// over every level. One page in the board, reached from its top bar,
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
  await expect(tree(main)).toBeVisible();
  return main;
}

const tree = (main: Locator) => main.getByRole("navigation", { name: "Where connectors live" });

// Every card of Needs a look has the same parts in the same order, its
// actions last in one row at its foot with Remove last, and cards side by side
// share their top and their height.
async function expectSameCards(attention: Locator) {
  const parts = await attention.locator(".look-card").evaluateAll((cards) => cards.map((card) => {
    const children = [...card.children].map((child) => child.className).filter((name) => name !== "look-card-vars");
    const actions = card.querySelector(".look-card-actions")!, last = actions.querySelector(".look-card-end > :last-child")!;
    const box = card.getBoundingClientRect(), foot = actions.getBoundingClientRect(), remove = last.getBoundingClientRect();
    return {
      children, last: last.textContent, isAtFoot: box.bottom - foot.bottom < 20, isRightmost: foot.right - remove.right < 2,
      top: Math.round(box.top), height: Math.round(box.height),
    };
  }));
  for (const card of parts) {
    expect(card.children).toEqual(["look-card-head", "look-card-state", "look-card-facts", "look-card-actions"]);
    expect(card.last).toBe("Remove");
    expect(card.isAtFoot && card.isRightmost, "the actions sit at the card's foot with Remove at its right").toBe(true);
  }
  for (const [index, card] of parts.entries()) {
    const beside = parts.find((other, at) => at !== index && other.top === card.top);
    if (beside) expect(beside.height, "cards side by side are the same height").toBe(card.height);
  }
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
  const narrowNames = await main.locator(".connector-name").evaluateAll((names) => names.filter((name) => name.getBoundingClientRect().width < 150).map((name) => name.textContent));
  expect(narrowNames, "no row's buttons squeeze its name to a word a line").toEqual([]);
}

for (const width of [1440, 390]) {
  test(`the page's tree goes from this PC to a project to a goblin, each row tagged with its scope, and back up by its breadcrumb, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    const supervisor = fleet();
    const main = await open(page, supervisor, width);
    const isWide = width === 1440;
    const attention = group(main, "Needs a look");

    // Assert: the tree holds every scope, the widest first, each project with its goblins under it.
    await expect(tree(main).getByRole("button", { name: "Needs a look, 4" })).toBeVisible();
    await expect(tree(main).locator(".connector-leaf")).toHaveText(["Command-line tools3", "Harness sign-ins1", "User environment credentials1", "Built in to every goblin1"]);
    await expect(tree(main).locator(".connector-tag")).toHaveText(["Global", "Local", "Goblin", "Goblin", "Yours only"]);
    await expect(tree(main).locator(".connector-tree-list > li > .connector-node")).toHaveText(["acme2 private · 1 in repo", "globex1 private"]);
    await expect(tree(main).getByRole("button", { name: "globex, 1 private, needs a look" })).toBeVisible();
    await expect(tree(main).getByRole("button", { name: ZEKE + ", carries 1, needs a look" })).toBeVisible();
    await expect(tree(main).getByRole("button", { name: "Ivy - Window Painter, carries 0" })).toBeVisible();
    await expect(tree(main).getByRole("button", { name: "Your Claude and Codex, 1" })).toBeVisible();

    // Assert: where the page is wide Needs a look shows beside the tree; on a phone the tree is the home alone.
    if (isWide) await expect(attention).toBeVisible();
    else await expect(attention).toBeHidden();
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-home-${width}.png`) });

    // Act: Needs a look.
    await tree(main).getByRole("button", { name: "Needs a look, 4" }).click();

    // Assert: what needs a look from every scope, each card tagged with its scope.
    if (isWide) await expect(tree(main).getByRole("button", { name: "Needs a look, 4" })).toHaveAttribute("aria-current", "location");
    await expect(attention.locator(".connector-open")).toHaveText(["gcloud", "ACME_API_KEY", "Supabase", "Acme Docs"]);
    await expect(attention.locator(".connector-tag")).toHaveText(["Global", "Global", "Local", "Goblin"]);
    await expect(attention.locator(".look-card-state b")).toHaveText(["Not checked", "Every goblin has it", "Missing", "Failed to load"]);
    if (isWide) await expect(tree(main)).toBeVisible();
    else await expect(tree(main)).toBeHidden();
    await expectSameCards(attention);
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-look-${width}.png`) });

    // Act: back to the home by the breadcrumb, then this PC.
    await crumbs(main).getByRole("button", { name: "Connectors", exact: true }).click();
    await tree(main).getByRole("button", { name: "This PC", exact: true }).click();

    // Assert: what every goblin gets, the harness sign-ins with it, each row Global.
    await expect(crumbs(main).locator(".here")).toHaveText("This PC");
    await expect(main.locator(".connector-title")).toContainText("This PCGlobal");
    await expect(main.locator(".connector-group > h4")).toHaveText(["Command-line tools3", "Harness sign-ins1", "User environment credentials1", "Built in to every goblin1"]);
    await expect(group(main, "Command-line tools").locator(".connector-name strong")).toHaveText(["gh", "vercel", "gcloud"]);
    await expect(group(main, "Command-line tools").locator(".connector-state b")).toHaveText(["Verified", "Installed", "Not checked"]);
    await expect(group(main, "Harness sign-ins").locator(".connector-tag")).toHaveText("Global");
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-pc-${width}.png`) });

    // Act: back by the breadcrumb, which lands on the way down, then your own Claude and Codex.
    await crumbs(main).getByRole("button", { name: "Connectors", exact: true }).click();
    await expect(tree(main).getByRole("button", { name: "This PC", exact: true })).toBeFocused();
    await tree(main).getByRole("button", { name: "Your Claude and Codex, 1" }).click();

    // Assert: what only your own sessions load.
    await expect(crumbs(main).locator(".here")).toHaveText("Your Claude and Codex");
    await expect(group(main, "Comes with your Claude account").locator(".connector-state b")).toHaveText("Not given");
    await expect(group(main, "Comes with your Claude account").locator(".connector-tag")).toHaveText("Yours only");

    // Act: the projects.
    await crumbs(main).getByRole("button", { name: "Connectors", exact: true }).click();
    await tree(main).getByRole("button", { name: "Projects, 2, one git repository each" }).click();

    // Assert: each project once, those with a goblin first, with how much it holds.
    await expect(group(main, "Every project").locator(".connector-open")).toHaveText(["acme", "globex"]);
    await expect(group(main, "Every project").locator(".connector-sub")).toHaveText(["2 services · 1 MCP server · 1 goblin", "1 service · 1 goblin"]);
    await expect(group(main, "Every project").locator(".connector-stale")).toHaveCount(1);

    // Act: a project.
    await group(main, "Every project").getByRole("button", { name: "acme", exact: true }).click();

    // Assert: its repository's servers apart from what it keeps private to this PC, each saying who carries it.
    await expect(crumbs(main).locator(".here")).toHaveText("acme");
    await expect(main.locator(".connector-title")).toHaveText("acmeLocalC:/work/acme · one git repository");
    const repository = group(main, "In the repository");
    await expect(repository.locator(".connector-where-head")).toContainText("In the repository.mcp.json1 server");
    await expect(repository.locator(".connector-state b")).toHaveText("Not given");
    await expect(repository.locator(".connector-sub")).toHaveText("Supabase project …k3tv · also a service below");
    const kept = group(main, "Private to this PC");
    await expect(kept.locator(".connector-where-head")).toContainText("Private to this PCauth.json2 services");
    await expect(kept.locator(".connector-name strong")).toHaveText(["GitHub", "Supabase"]);
    await expect(kept.locator(".connector-state b")).toHaveText(["Verified", "Set"]);
    await expect(kept.locator(".connector-sub")).toHaveText(["read from its probe · " + ZEKE + " carries it", "Supabase project …k3tv · no goblin carries it · also a server above"]);
    await expect(kept.getByRole("list", { name: "Credential names of Supabase" }).locator("li")).toHaveText(["SUPABASE_URLset", "SUPABASE_SERVICE_KEYnot set"]);
    await expect(kept.locator(".connector-tag").last()).toHaveText("Local");
    await expect(group(main, "From this PC")).toContainText("3 tools, 1 harness sign-in, 1 credential and 1 built-in, the same for every project.");
    await expect(group(main, "Goblins").locator(".connector-open")).toHaveText(ZEKE);
    if (isWide) await expect(tree(main).getByRole("button", { name: "acme, 2 private · 1 in repo" })).toHaveAttribute("aria-current", "location");
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-project-${width}.png`) });

    // Act: a goblin.
    await group(main, "Goblins").getByRole("button", { name: ZEKE, exact: true }).click();

    // Assert: it sits under its project, and reads from the widest scope down.
    await expect(crumbs(main).locator("button, .here")).toHaveText(["Connectors", "Projects", "acme", ZEKE]);
    await expect(main.locator(".connector-where")).toHaveCount(3);
    await expect(group(main, "From this PC")).toContainText("Every goblin gets these: 3 tools, 1 harness sign-in, 1 credential and 1 built-in.");
    const granted = group(main, "Granted from acme");
    await expect(granted.locator(".connector-chip")).toHaveText("1 of 2");
    await expect(granted.getByRole("button", { name: "Disconnect GitHub from " + ZEKE })).toBeVisible();
    await expect(granted.getByRole("button", { name: "Connect Supabase to " + ZEKE })).toHaveCount(0);
    const session = group(main, "Its session");
    await expect(session.locator(".connector-chip")).toHaveText("1 failed");
    await expect(session.locator(".connector-state b")).toHaveText(["Failed to load", "Loaded"]);
    await expect(session.locator(".connector-state").first()).toContainText("Connection closed");
    await expect(session.locator("summary")).toHaveText("Tools2");
    await expect(main.locator(".connector-row .connector-refresh")).toHaveCount(0);
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-goblin-${width}.png`) });

    // Act: the services it is not given unfold.
    await granted.getByText("1 service of acme it is not given", { exact: true }).click();

    // Assert
    await expect(granted.getByRole("button", { name: "Connect Supabase to " + ZEKE })).toBeVisible();

    // Act: back up by the breadcrumb, a step at a time.
    await crumbs(main).getByRole("button", { name: "acme", exact: true }).click();

    // Assert: the way back lands on the row it came down by.
    await expect(crumbs(main).locator(".here")).toHaveText("acme");
    await expect(group(main, "Goblins").getByRole("button", { name: ZEKE, exact: true })).toBeFocused();

    // Act: down to the goblin again, then up two steps at once.
    await group(main, "Goblins").getByRole("button", { name: ZEKE, exact: true }).click();
    await crumbs(main).getByRole("button", { name: "Projects", exact: true }).click();

    // Assert: the way back lands on the project it went down through.
    await expect(group(main, "Every project").getByRole("button", { name: "acme", exact: true })).toBeFocused();
    expect(supervisor.posts).toEqual([]);

    // Act: Needs a look again, from the tree.
    await crumbs(main).getByRole("button", { name: "Connectors", exact: true }).click();
    await tree(main).getByRole("button", { name: "Needs a look, 4" }).click();

    // Act: remove the project's missing service, confirming what it changes.
    await attention.getByRole("button", { name: "Remove Supabase, globex" }).click();
    const confirm = page.getByRole("dialog", { name: "Remove Supabase?" });
    await expect(confirm).toContainText("Takes the service supabase out of globex's auth.json");
    await page.screenshot({ path: testInfo.outputPath(`connectors-remove-${width}.png`) });
    await confirm.getByRole("button", { name: "Remove", exact: true }).click();

    // Assert: the one service is removed and leaves the list and the tree's count, the rest stay.
    await expect(confirm).toBeHidden();
    await expect(attention.locator(".connector-open")).toHaveText(["gcloud", "ACME_API_KEY", "Acme Docs"]);
    if (isWide) await expect(tree(main).getByRole("button", { name: "Needs a look, 3" })).toBeVisible();
    expect(supervisor.posts.map((post) => [post.path, post.token, post.body])).toEqual([["/api/connectors/remove", "fixture", { level: "project", project: "globex", task: "", generation: "", connection: "service:supabase" }]]);
  });

  test(`a goblin opens straight from the tree, which marks it, and the way back lands on it, at ${width}px`, async ({ page }) => {
    // Arrange
    const main = await open(page, fleet(), width);

    // Act
    await tree(main).getByRole("button", { name: "Ivy - Window Painter, carries 0" }).click();

    // Assert: the goblin's view takes the focus.
    await expect(crumbs(main).locator(".here")).toHaveText("Ivy - Window Painter");
    await expect(main.getByRole("region", { name: "Ivy - Window Painter view" })).toBeFocused();
    if (width === 1440) await expect(tree(main).getByRole("button", { name: "Ivy - Window Painter, carries 0" })).toHaveAttribute("aria-current", "location");
    else await expect(tree(main)).toBeHidden();

    // Act: across to another project from the tree, where the page is wide, then home by the breadcrumb.
    if (width === 1440) {
      await tree(main).getByRole("button", { name: "acme, 2 private · 1 in repo" }).click();
      await expect(main.getByRole("region", { name: "acme view" })).toBeFocused();
      await tree(main).getByRole("button", { name: "Ivy - Window Painter, carries 0" }).click();
    }
    await crumbs(main).getByRole("button", { name: "Connectors", exact: true }).click();

    // Assert: the way back lands on the goblin it went down by.
    await expect(tree(main).getByRole("button", { name: "Ivy - Window Painter, carries 0" })).toBeFocused();
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
    await expect(main.locator(".connector-group > h4")).toHaveText(["Account1", "Project3", "Goblin1"]);
    const projects = group(main, "Project");
    await expect(projects.locator(".connector-sub")).toHaveText(["acme · Supabase project …k3tv", "acme · Supabase project …k3tv", "globex · Supabase project …q7wk"]);
    await expect(projects.getByRole("button", { name: "Supabase, globex" })).toBeVisible();
    await expect(main.getByText("5 matches, searched in the saved snapshot: nothing was checked to answer it.")).toBeVisible();
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-search-${width}.png`) });

    // Act: a word that every level holds.
    await search.fill("acme");

    // Assert
    await expect(main.locator(".connector-group > h4")).toHaveText(["This PC1", "Project3", "Goblin4"]);

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
    await expect(tree(main)).toBeVisible();
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
      await expect(tree(main)).toBeVisible();
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
  await tree(main).getByRole("button", { name: "acme, 2 private · 1 in repo" }).click();

  // Assert
  await expect(main.getByText("No goblin is running in this project.", { exact: true })).toBeVisible();
  await expect(main.getByRole("region", { name: "Private to this PC" })).toBeVisible();
});
