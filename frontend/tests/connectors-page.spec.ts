import { CHECKED_AT, play, supervisorWith, type Supervisor } from "./connectors-supervisor";
import { expect, holdStream, openTaskList, taskList, test, type Locator, type Page } from "./site";

// The Overlord, 2026-11-10: "I also don't like the connectors panel inside
// the task panels for every goblin. It doesn't look like Codex or Claude
// Code's connectors panel, which is much, much cleaner, in the way that you
// can navigate it and use it" (#2303). One Connectors page, reached from the
// board's top bar: a list with a search and a Yours and Discover split, and
// each connector's own page. The supervisor is played here.
const goblin = (id: string, name: string, title: string) => ({
  id, title: "A made-up task of " + name, goblin_name: name, goblin_title: title, project: "code-goblins", phase: "working", verified: false,
  generation: id + "-1", harness: "claude", model: "claude-opus-5-5", effort: "xhigh", backend: "native", since: "2026-11-10T09:00:00Z",
});
const BOARD = {
  instance: "fixture", healthy: true, revision: 1, ceo_runs: true, ceo_terminal: "ceo", ceo_terminal_since: "2026-11-10T09:00:00Z", ceo_harness: "claude",
  sessions: [{ id: "ceo-1", harness: "claude", role: "ceo", model: "claude-opus-5-5" }],
  tasks: [goblin("cg-touch", "Zeke", "Touch Wrangler"), goblin("cg-other", "Ivy", "Window Painter")],
};
const ZEKE = "Zeke - Touch Wrangler", IVY = "Ivy - Window Painter";
const REPLACE_NOTE = "Replace from clipboard stores what you copied in its place, for every goblin of that repository, and shows neither value. Running goblins are told to load it again. Set means a goblin holds a value, not which one.";
const TOOLS = [{ name: "list_projects", read_only: true }, { name: "list_tables", read_only: true }, { name: "supabase_get_project_url", read_only: true }, { name: "execute_sql", read_only: false }, { name: "apply_migration" }];
const SUPABASE = { id: "mcp:claude.ai Supabase", name: "claude.ai Supabase", kind: "mcp", status: "connected", source: "Claude", checked_at: CHECKED_AT, tools: TOOLS };
const CRAWLER = { id: "connector:crawler", name: "crawler", kind: "connector", status: "provided", source: "Code Goblins", checked_at: CHECKED_AT };
const withheld = (name: string) => ({ id: "service:" + name, name, kind: "service", status: "withheld", source: "Repository", detail: "Not currently granted to this goblin.", checked_at: CHECKED_AT, grant: "grant" });
const carried = (name: string) => ({ id: "service:" + name, name, kind: "service", status: "connected", source: "Repository", checked_at: CHECKED_AT, grant: "revoke" });
const fleet = () => supervisorWith({
  "cg-touch": [
    SUPABASE,
    { id: "mcp:context7", name: "context7", kind: "mcp", status: "unauthorized", detail: "Sign-in required.", actions: ["login"], checked_at: CHECKED_AT },
    { id: "mcp:chrome-devtools", name: "chrome-devtools", kind: "mcp", status: "probe_succeeded", scope: "separate_probe", source: "Claude", checked_at: CHECKED_AT },
    { id: "mcp:acme-internal", name: "acme-internal", kind: "mcp", status: "connected", checked_at: CHECKED_AT },
    { ...carried("github"), restart_required: true, restart_reason: "Connected. Its missing variables reach this goblin at its next terminal. The one running started without them." },
    withheld("sentry"), CRAWLER,
    { id: "credential:GITHUB_TOKEN", name: "GITHUB_TOKEN", kind: "credential", status: "provided", scope: "launch_environment", accounts: ["github"], actions: ["replace:GITHUB_TOKEN"], checked_at: CHECKED_AT },
    { id: "credential:SENTRY_AUTH_TOKEN", name: "SENTRY_AUTH_TOKEN", kind: "credential", status: "withheld", detail: "This task does not carry it.", accounts: ["sentry"], checked_at: CHECKED_AT },
  ],
  "cg-other": [SUPABASE, { ...withheld("github"), restart_required: true, restart_reason: "Disconnected. Its variables leave this goblin at its next terminal. The one running still has them." }, withheld("sentry"), CRAWLER,
    { id: "credential:GITHUB_TOKEN", name: "GITHUB_TOKEN", kind: "credential", status: "withheld", detail: "This task does not carry it.", accounts: ["github"], checked_at: CHECKED_AT }],
});

// The board, with the Connectors page chosen from its top bar.
async function open(page: Page, supervisor: Supervisor, width: number): Promise<Locator> {
  await page.setViewportSize({ width, height: 1100 });
  await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
  await holdStream(page, BOARD);
  await play(page, supervisor);
  await page.goto("/");
  await page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Connectors", exact: true }).click();
  const main = page.getByRole("main", { name: "Connectors" });
  await expect(main.getByRole("heading", { name: "Connectors", exact: true })).toBeVisible();
  await expect(main.getByText("Checking connectors...", { exact: true })).toHaveCount(0);
  return main;
}

const row = (main: Locator, name: string) => main.locator(".connector-row").filter({ has: main.page().getByRole("button", { name, exact: true }) });
const tab = (main: Locator, name: string) => main.getByRole("group", { name: "Connectors shown" }).getByRole("button", { name: new RegExp("^" + name) });
const goblinRow = (detail: Locator, name: string) => detail.locator(".connector-goblin").filter({ hasText: name });

// Nothing on the page is wider than its place, and none of its words is small.
async function expectFits(page: Page, main: Locator) {
  await page.evaluate(() => document.fonts.ready);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "the page does not scroll sideways").toBe(true);
  expect(await main.evaluate((element) => element.scrollWidth <= element.clientWidth), "the canvas does not scroll sideways").toBe(true);
  const small = await main.locator(".connectors-page").evaluate((element) => [...element.querySelectorAll("h2, h3, p, span, strong, button, input, summary, li, time")]
    .filter((node) => node.textContent?.trim() && parseFloat(getComputedStyle(node).fontSize) < 16).map((node) => node.textContent));
  expect(small).toEqual([]);
}

for (const width of [1440, 390]) {
  test(`the list names each connector once with its logo, its type and its status, split into Yours and Discover, and a search narrows it, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    const supervisor = fleet();

    // Act
    const main = await open(page, supervisor, width);

    // Assert: what a goblin carries is Yours, each once, by its proper name.
    const yours = main.getByRole("list", { name: "Your connectors" });
    await expect(yours.locator(".connector-open")).toHaveText(["Acme Internal", "Chrome DevTools", "Context7", "Crawler", "GitHub", "GITHUB_TOKEN", "Supabase"]);
    await expect(yours.locator(".connector-type")).toHaveText(["MCP server", "MCP server", "MCP server", "Built in", "Repository service", "CLI variable", "Claude MCP connector"]);
    const logo = row(main, "Supabase").locator(".connector-logo img");
    await expect(logo).toHaveAttribute("src", "/assets/connectors/supabase.svg");
    await expect(logo).toHaveAttribute("loading", "lazy");
    await expect.poll(() => logo.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBe(true);
    await expect(row(main, "GITHUB_TOKEN").locator(".connector-logo img")).toHaveAttribute("src", "/assets/connectors/github.svg");
    await expect(row(main, "Acme Internal").locator(".connector-initial")).toHaveText("A");
    await expect(row(main, "Acme Internal").locator("img")).toHaveCount(0);

    // Assert: each state in one word, Verified with a check only where a
    // check confirmed it and Set for a variable no check can confirm, and a
    // sign-in a goblin needs a button in the board's look, as Connect is,
    // never coloured words (the Overlord, 2026-11-11).
    await expect(yours.locator(".connector-status")).toHaveText(["Verified", "Verified", "Sign in", "Installed", "Verified", "Set", "Verified"]);
    await expect(row(main, "Supabase").locator(".connector-state.is-verified .icon")).toHaveCount(1);
    await expect(row(main, "Context7").locator(".connector-status button.connector-connect")).toHaveAccessibleName("Sign in to Context7");
    await expect(row(main, "Context7").locator(".connector-status button"), "the board's green button").toHaveClass(/\bprimary\b/);
    await expect(row(main, "Context7").locator(".connector-state, .connector-status .icon")).toHaveCount(0);
    await expect(row(main, "GITHUB_TOKEN").locator(".connector-state.is-quiet")).toHaveText("Set");
    // Assert: only Verified is green, and the rest, with a change waiting for
    // a goblin's next terminal, is as quiet as a type.
    const colour = (locator: Locator) => locator.evaluate((element) => getComputedStyle(element).color);
    const quiet = await colour(row(main, "GitHub").locator(".connector-type"));
    expect(await colour(row(main, "Supabase").locator(".connector-state")), "verified and quiet differ").not.toBe(quiet);
    expect(await colour(row(main, "GITHUB_TOKEN").locator(".connector-state"))).toBe(quiet);
    expect(await colour(row(main, "Crawler").locator(".connector-state"))).toBe(quiet);
    await expect(row(main, "GitHub").locator(".connector-waiting")).toHaveText("Applies at its next terminal");
    expect(await colour(row(main, "GitHub").locator(".connector-waiting"))).toBe(quiet);
    await expect(main.locator(".connector-waiting")).toHaveCount(1);
    await expect(row(main, "Supabase").getByRole("button", { name: /^Connect / })).toHaveCount(0);
    await expect(main).not.toContainText("claude.ai");
    await expect(main).not.toContainText("SENTRY_AUTH_TOKEN");
    await expect(tab(main, "Yours")).toHaveText("Yours7");
    await expect(tab(main, "Discover")).toHaveText("Discover1");
    expect(supervisor.reads).toEqual({ "cg-touch": 1, "cg-other": 1 });
    // Assert: with no goblin chosen, no panel asks for a task beside the page.
    await expect(page.locator("aside.context-pane")).toBeHidden();
    await expect(page.getByRole("button", { name: /^Open (CEO|details)$/ })).toHaveCount(0);
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-list-${width}.png`) });

    // Act: what no goblin carries is under Discover, with Connect.
    await tab(main, "Discover").click();

    // Assert
    await expect(main.getByRole("list", { name: "Connectors to discover" }).locator(".connector-open")).toHaveText(["Sentry"]);
    await expect(row(main, "Sentry").getByRole("button", { name: "Connect Sentry", exact: true })).toBeVisible();
    await expect(main.getByRole("list", { name: "Your connectors" })).toHaveCount(0);
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connectors-discover-${width}.png`) });

    // Act: a search narrows both lists.
    await tab(main, "Yours").click();
    await main.getByRole("searchbox", { name: "Search connectors" }).fill("supa");

    // Assert
    await expect(yours.locator(".connector-open")).toHaveText(["Supabase"]);
    await expect(tab(main, "Discover")).toHaveText("Discover0");

    // Act
    await main.getByRole("searchbox", { name: "Search connectors" }).fill("zzz");

    // Assert
    await expect(main.getByText("Nothing matches \"zzz\".", { exact: true })).toBeVisible();
    await expect(yours).toHaveCount(0);
  });

  test(`a connector's own page says what it is, which goblins can use it and its tools in two groups, and its way back returns to the list, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    const main = await open(page, fleet(), width);
    await main.getByRole("searchbox", { name: "Search connectors" }).fill("s");

    // Act
    await row(main, "Supabase").getByRole("button", { name: "Supabase", exact: true }).click();

    // Assert: its logo, name, type and what it does, with the way back first.
    const detail = main.getByRole("article", { name: "Supabase" });
    await expect(main.getByRole("button", { name: "Connectors", exact: true })).toBeFocused();
    await expect(detail.getByRole("heading", { name: "Supabase", exact: true })).toBeVisible();
    await expect(detail.locator(".connector-head .connector-type")).toHaveText("Claude MCP connector");
    await expect(detail.locator(".connector-head .connector-logo img")).toHaveAttribute("src", "/assets/connectors/supabase.svg");
    await expect(detail.locator(".connector-about")).toHaveText("Supabase database, auth and storage");
    await expect(main.getByRole("list", { name: "Your connectors" })).toBeHidden();

    // Assert: the goblins that can use it.
    const goblins = detail.getByRole("list", { name: "Goblins that can use Supabase" });
    await expect(goblins.getByRole("listitem")).toHaveCount(2);
    // Assert: Verified says nothing more beside it; how it was checked is in
    // the check details at the bottom (the Overlord, 2026-11-11).
    await expect(goblinRow(goblins, ZEKE).locator(".connector-use")).toHaveText("Verified");
    await expect(goblinRow(goblins, IVY).locator(".connector-use .connector-word")).toHaveText("Verified");
    await expect(goblins.getByRole("button")).toHaveCount(0);

    // Assert: it reaches a goblin as an MCP server, which its part says, and
    // brings no CLI variables.
    await expect(detail.getByRole("region", { name: "MCP server" }).locator(".connector-part-note")).toHaveText("The goblin's harness starts it and calls its tools.");
    await expect(detail.getByRole("region", { name: "CLI variables" })).toHaveCount(0);

    // Assert: its tools wait closed, in two groups with their counts.
    const groups = detail.locator("details.connector-tool-group");
    await expect(groups.locator("> summary")).toHaveText(["Read-only tools3", "Write tools2"]);
    await expect(detail.getByRole("list", { name: "Read-only tools" })).toHaveCount(0);

    // Act
    await groups.nth(0).locator("> summary").click();
    await groups.nth(1).locator("> summary").click();
    await detail.locator("details.connector-checks > summary").click();

    // Assert: each tool in words, and for each goblin what its check proved
    // and did not, when it ran, and what it was run against.
    await expect(detail.getByRole("list", { name: "Read-only tools" }).getByRole("listitem")).toHaveText(["Get project URL", "List projects", "List tables"]);
    await expect(detail.getByRole("list", { name: "Write tools" }).getByRole("listitem")).toHaveText(["Apply migration", "Execute SQL"]);
    await expect(detail.locator("details.connector-checks")).toContainText(ZEKE + " How it was checked is unknown. Not checked: whether the goblin's own session can use it, or which account and project it reaches. Checked " + CHECKED_AT + ". Generation cg-touch-1. Verified process 42, started 2026-11-10T14:00:00Z, workspace C:/scratch/work.");
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connector-detail-${width}.png`), fullPage: true });

    // Act
    await main.getByRole("button", { name: "Connectors", exact: true }).click();

    // Assert: the list as he left it, with the keyboard on the row he opened.
    await expect(detail).toHaveCount(0);
    await expect(main.getByRole("heading", { name: "Connectors", exact: true })).toBeVisible();
    await expect(main.getByRole("searchbox", { name: "Search connectors" })).toHaveValue("s");
    await expect(row(main, "Supabase").getByRole("button", { name: "Supabase", exact: true })).toBeFocused();

    // Act: the list's Sign in opens the connector a goblin must sign in to.
    await row(main, "Context7").getByRole("button", { name: "Sign in to Context7", exact: true }).click();

    // Assert: the goblin says why beside its own Sign in, with no amber word.
    const context7 = main.getByRole("article", { name: "Context7" });
    await expect(goblinRow(context7, ZEKE).locator(".connector-use")).toHaveText("Sign-in required.");
    await expect(goblinRow(context7, ZEKE).getByRole("button", { name: "Sign in to Context7 for " + ZEKE, exact: true }), "the board's green button, as Connect is").toHaveClass(/\bprimary\b/);
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connector-context7-${width}.png`), fullPage: true });
  });
}

for (const width of [1440, 390]) {
  test(`a probe that succeeded says what it proves and when, a change waiting for a goblin's next terminal says why under its name, and a connector with no tools lists none, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    const main = await open(page, fleet(), width);

    // Act
    await row(main, "Chrome DevTools").getByRole("button", { name: "Chrome DevTools", exact: true }).click();

    // Assert: Verified alone beside the goblin, and in the check details what
    // its probe proved and did not and when it ran.
    const probed = main.getByRole("article", { name: "Chrome DevTools" });
    await expect(goblinRow(probed, ZEKE).locator(".connector-use")).toHaveText("Verified");
    await probed.locator("details.connector-checks > summary").click();
    await expect(probed.locator("details.connector-checks")).toContainText(ZEKE + " A separate check with its credentials succeeded. Not checked: whether the goblin's own session can use it, or which account and project it reaches. Checked " + CHECKED_AT + ".");
    await expect(probed.locator("details.connector-checks time")).toHaveAttribute("datetime", CHECKED_AT);
    await expect(probed.getByRole("listitem")).toHaveCount(1);
    await expect(probed.getByRole("region", { name: "MCP server" })).toBeVisible();
    await expect(probed.locator("details.connector-tool-group")).toHaveCount(0);

    // Act
    await main.getByRole("button", { name: "Connectors", exact: true }).click();
    await row(main, "GitHub").getByRole("button", { name: "GitHub", exact: true }).click();

    // Assert: the change waiting for each goblin's next terminal says why,
    // quietly, the Disconnect of one that no longer carries it as much as the
    // Connect of one that does.
    const github = main.getByRole("article", { name: "GitHub" });
    await expect(goblinRow(github, ZEKE).locator(".connector-waiting")).toHaveText("Connected. Its missing variables reach this goblin at its next terminal. The one running started without them.");
    await expect(goblinRow(github, ZEKE).getByRole("button", { name: "Disconnect GitHub from " + ZEKE, exact: true })).toBeVisible();
    await expect(goblinRow(github, IVY)).toContainText("Not connected");
    await expect(goblinRow(github, IVY).locator(".connector-waiting")).toHaveText("Disconnected. Its variables leave this goblin at its next terminal. The one running still has them.");
    await expect(goblinRow(github, IVY).getByRole("button", { name: "Connect GitHub to " + IVY, exact: true })).toBeVisible();
    // Assert: each goblin's name, its state and why its change waits are lines
    // one under another, at every width.
    for (const name of [ZEKE, IVY]) {
      const lines = await goblinRow(github, name).locator(".connector-goblin-copy > *").evaluateAll((parts) => parts.map((part) => {
        const box = part.getBoundingClientRect();
        return { left: box.left, top: box.top, bottom: box.bottom };
      }));
      expect(lines, name + " has a name, a state and a reason").toHaveLength(3);
      for (const [at, line] of lines.slice(1).entries()) {
        expect(line.left, name + "'s line " + (at + 2) + " starts under its name").toBeCloseTo(lines[0].left, 0);
        expect(line.top, name + "'s line " + (at + 2) + " is below the one before it").toBeGreaterThanOrEqual(lines[at].bottom - 0.5);
      }
    }

    // Assert: it brings its CLI variables, each by name with what each goblin's
    // terminal holds of it, never a value, and no MCP server.
    const variables = github.getByRole("region", { name: "CLI variables" });
    await expect(variables.locator(".connector-part-note")).toHaveText("Command line tools in the goblin's terminal read them. Names only, never values.");
    await expect(variables.getByRole("listitem")).toHaveText(["GITHUB_TOKEN" + ZEKE + ": Set, " + IVY + ": Not given"]);
    await expect(github.getByRole("region", { name: "MCP server" })).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath(`connector-github-${width}.png`), fullPage: true });

    // Act
    await main.getByRole("button", { name: "Connectors", exact: true }).click();
    await row(main, "GITHUB_TOKEN").getByRole("button", { name: "GITHUB_TOKEN", exact: true }).click();

    // Assert: a variable is Set, never Verified, and names the service it is from.
    const token = main.getByRole("article", { name: "GITHUB_TOKEN" });
    await expect(goblinRow(token, ZEKE).locator(".connector-use")).toHaveText("Set");
    await token.locator("details.connector-checks > summary").click();
    await expect(token.locator("details.connector-checks")).toContainText(ZEKE + " The goblin's environment has it. Not checked: whether the goblin's own session can use it, or which account and project it reaches. Checked " + CHECKED_AT + ".");
    await expect(token.getByRole("region", { name: "CLI variables" }).locator(".connector-part-note")).toHaveText(["Command line tools in the goblin's terminal read them. Names only, never values.", "Declared by the GitHub repository service.", REPLACE_NOTE]);
  });
}

// The Overlord, 2026-11-11, on the cleaned page: change or reset a variable
// without peeking at it (#2389). A set variable offers Replace from clipboard
// beside each goblin that holds it, which asks for the same clipboard store as
// Store from clipboard, and a refused one says why.
for (const width of [1440, 390]) {
  test(`a set variable offers Replace from clipboard beside each goblin that holds it, never showing a value, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    const supervisor = fleet();
    const main = await open(page, supervisor, width);

    // Act
    await row(main, "GITHUB_TOKEN").getByRole("button", { name: "GITHUB_TOKEN", exact: true }).click();

    // Assert: Replace beside the goblin that holds it, in Store from
    // clipboard's look, none beside the one not given it, and the page says
    // what it does.
    const token = main.getByRole("article", { name: "GITHUB_TOKEN" });
    const replace = goblinRow(token, ZEKE).getByRole("button", { name: "Replace GITHUB_TOKEN from clipboard for " + ZEKE, exact: true });
    await expect(replace).toHaveText("Replace GITHUB_TOKEN from clipboard");
    await expect(replace).not.toHaveClass(/\bprimary\b/);
    await expect(goblinRow(token, ZEKE).locator(".connector-use")).toHaveText("Set");
    await expect(goblinRow(token, IVY).getByRole("button")).toHaveCount(0);
    await expect(token.getByRole("region", { name: "CLI variables" }).locator(".connector-part-note").last()).toHaveText(REPLACE_NOTE);
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connector-replace-${width}.png`), fullPage: true });

    // Act
    supervisor.refuses = "This repair is no longer available. Refresh connections.";
    await replace.click();

    // Assert: it asks for that goblin's clipboard store, and a refusal is said.
    await expect.poll(() => supervisor.posts).toEqual([{ path: "/api/connections/fix", token: "fixture", body: { task: "cg-touch", generation: "cg-touch-1", connection: "credential:GITHUB_TOKEN", action: "replace:GITHUB_TOKEN" } }]);
    await expect(main.locator(".connectors-page > .notice")).toHaveText("This repair is no longer available. Refresh connections.");
  });
}

for (const width of [1440, 390]) {
  test(`a connector no goblin carries offers Connect, which gives it to the goblin he picks at once, and Disconnect takes it back, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    const supervisor = fleet();
    let release = () => {};
    supervisor.held = new Promise<void>((resolve) => { release = resolve; });
    const main = await open(page, supervisor, width);
    await tab(main, "Discover").click();

    // Act
    await main.getByRole("button", { name: "Connect Sentry", exact: true }).click();

    // Assert: its page, with no goblin connected and Connect beside each.
    const detail = main.getByRole("article", { name: "Sentry" });
    await expect(detail.locator(".connector-head .connector-type")).toHaveText("Repository service");
    await expect(detail.locator(".connector-about")).toHaveText("Errors and performance");
    await expect(goblinRow(detail, ZEKE)).toContainText("Not connected");
    await expect(goblinRow(detail, IVY)).toContainText("Not connected");
    await expect(detail).not.toContainText("Not currently granted");
    await expectFits(page, main);
    await page.screenshot({ path: testInfo.outputPath(`connector-disconnected-${width}.png`) });

    // Act
    await detail.getByRole("button", { name: "Connect Sentry to " + ZEKE, exact: true }).click();

    // Assert: the goblin's row changes before the supervisor has answered.
    await expect(detail.getByRole("button", { name: "Disconnect Sentry from " + ZEKE, exact: true })).toBeVisible();
    await expect(goblinRow(detail, IVY).getByRole("button", { name: "Connect Sentry to " + IVY, exact: true })).toBeVisible();
    await expect.poll(() => supervisor.posts).toEqual([{ path: "/api/connections/grant", token: "fixture", body: { task: "cg-touch", generation: "cg-touch-1", connection: "service:sentry", action: "grant" } }]);

    // Act: the check that follows finds it carried.
    supervisor.entries["cg-touch"] = supervisor.entries["cg-touch"].map((entry) => (entry as { id: string }).id === "service:sentry" ? carried("sentry") : entry);
    release();

    // Assert
    await expect(goblinRow(detail, ZEKE).locator(".connector-word")).toHaveText("Verified");
    await expect(detail.getByRole("button", { name: "Disconnect Sentry from " + ZEKE, exact: true })).toBeEnabled();
    await expect(goblinRow(detail, IVY)).toContainText("Not connected");

    // Act: back in the list it is one of his.
    await main.getByRole("button", { name: "Connectors", exact: true }).click();

    // Assert
    await expect(tab(main, "Discover")).toHaveText("Discover0");
    await tab(main, "Yours").click();
    await expect(row(main, "Sentry").locator(".connector-state.is-verified")).toHaveText("Verified");

    // Act: Disconnect takes it back.
    await row(main, "Sentry").getByRole("button", { name: "Sentry", exact: true }).click();
    supervisor.entries["cg-touch"] = supervisor.entries["cg-touch"].map((entry) => (entry as { id: string }).id === "service:sentry" ? withheld("sentry") : entry);
    await detail.getByRole("button", { name: "Disconnect Sentry from " + ZEKE, exact: true }).click();

    // Assert
    await expect(detail.getByRole("button", { name: "Connect Sentry to " + ZEKE, exact: true })).toBeVisible();
    await expect(goblinRow(detail, ZEKE)).toContainText("Not connected");
    expect(supervisor.posts[1]).toEqual({ path: "/api/connections/grant", token: "fixture", body: { task: "cg-touch", generation: "cg-touch-1", connection: "service:sentry", action: "revoke" } });
  });
}

test("a Connect the supervisor refuses puts the goblin's row back and says why beside it", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.refuses = "Change it in the Code Goblins window, or ask the CEO.";
  const main = await open(page, supervisor, 1440);
  await tab(main, "Discover").click();
  await main.getByRole("button", { name: "Connect Sentry", exact: true }).click();
  const detail = main.getByRole("article", { name: "Sentry" });

  // Act
  await detail.getByRole("button", { name: "Connect Sentry to " + IVY, exact: true }).click();

  // Assert
  await expect(main.locator(".connectors-page > .notice")).toHaveText("Change it in the Code Goblins window, or ask the CEO.");
  await expect(detail.getByRole("button", { name: "Connect Sentry to " + IVY, exact: true })).toBeVisible();
  await expect(goblinRow(detail, IVY)).toContainText("Not connected");
});

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
    await expect(main.getByRole("list", { name: "Your connectors" }).locator(".connector-open").first()).toBeVisible();
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
      await expect(main.getByRole("list", { name: "Your connectors" })).toBeVisible();
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

test("with no goblin running the page says it has nothing to read, and asks the supervisor nothing", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  await page.addInitScript(() => localStorage.setItem("ceo-first-open", "shown"));
  await holdStream(page, { ...BOARD, tasks: [{ ...goblin("cg-queued", "Quill", "Waiting"), generation: "", phase: "queued" }] });
  await play(page, supervisor);
  await page.goto("/");

  // Act
  await page.getByRole("group", { name: "Workspace view" }).getByRole("button", { name: "Connectors", exact: true }).click();

  // Assert
  const main = page.getByRole("main", { name: "Connectors" });
  await expect(main.getByText("No goblin is running. Connectors are read from the goblins that run.", { exact: true })).toBeVisible();
  await expect(main.getByRole("button", { name: "Recheck connectors" })).toBeDisabled();
  expect(supervisor.reads).toEqual({});
});
