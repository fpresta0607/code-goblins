import { bigFleet, fleet, minutesBefore, NOW, play, type Supervisor } from "./connectors-supervisor";
import { expect, test, type Page } from "./site";

// What the Connectors page asks the supervisor and when, and what it does with
// what he presses (#2902): it reads the saved snapshot once on open and sends
// no post; searching asks nothing; a refresh shows Rechecking and then the new
// time; a level past its life is Stale; a level being checked is read every
// 2 s up to a limit; Connect, Disconnect and a repair post what he chose; a
// project's browser logins are signed out of or forgotten only once asked; and
// the first view of a fleet the size of today's draws in under a second. The
// page stands alone here in the board's canvas, and the supervisor is played.
type Goblins = [string, string, string][];

async function open(page: Page, supervisor: Supervisor, goblins?: Goblins) {
  await page.clock.install({ time: Date.parse(NOW) });
  await play(page, supervisor);
  await page.goto("/tests/fixtures/connectors-page.html" + (goblins ? "?goblins=" + encodeURIComponent(JSON.stringify(goblins)) : ""));
  await expect(page.locator(".connector-tile").first()).toBeVisible();
}

const group = (title: string, page: Page) => page.getByRole("region", { name: title, exact: true });
const tile = (page: Page, level: number) => page.getByRole("button", { name: new RegExp("^Level " + level) });
const crumbs = (page: Page) => page.getByRole("navigation", { name: "Where you are" });
const view = (page: Page, name: string) => page.getByRole("region", { name: name + " view", exact: true });

// Opens a project from the home page.
async function openProject(page: Page, name: string) {
  await tile(page, 2).click();
  await page.getByRole("button", { name, exact: true }).click();
  await expect(crumbs(page).locator(".here")).toHaveText(name);
}

// Opens a goblin from the home page, through its project.
async function openGoblin(page: Page, project: string, name: string) {
  await openProject(page, project);
  await page.getByRole("button", { name, exact: true }).click();
  await expect(crumbs(page).locator(".here")).toHaveText(name);
}

const ZEKE = "Zeke - Touch Wrangler", IVY = "Ivy - Window Painter";

test("opening the page reads the saved snapshot once and posts nothing, and searching and moving between levels ask nothing more", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  await open(page, supervisor);
  expect(supervisor.requests).toEqual(["GET /api/connectors"]);

  // Act: search every level, then move through the levels, and let a long time pass.
  await page.getByRole("searchbox", { name: "Search connectors" }).fill("supabase");
  await expect(page.getByText("5 matches, searched in the saved snapshot", { exact: false })).toBeVisible();
  await page.getByRole("searchbox", { name: "Search connectors" }).fill("");
  await tile(page, 1).click();
  await crumbs(page).getByRole("button", { name: "Connectors", exact: true }).click();
  await openGoblin(page, "acme", ZEKE);
  await page.clock.runFor(10 * 60_000);

  // Assert: one read, no post, no check started by any of it.
  expect(supervisor.requests).toEqual(["GET /api/connectors"]);
  expect(supervisor.posts).toEqual([]);
});

test("a refresh posts the level and shows Rechecking until its check ends, then the new time", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.view.machine.checked_at = minutesBefore(30);
  for (const each of supervisor.view.machine.items) each.checked_at = minutesBefore(30);
  await open(page, supervisor);
  await tile(page, 1).click();
  const cli = group("CLI tools", page);
  await expect(cli.locator(".connector-state").first().locator("small").last()).toHaveText("checked 30 minutes ago");

  // Act
  await page.getByRole("button", { name: "Refresh this PC" }).click();

  // Assert: the machine's rows say so while it is checked, and none can be refreshed again.
  expect(supervisor.posts).toEqual([{ path: "/api/connectors/refresh", token: "connectors-proof", body: { level: "machine", scope: "" } }]);
  await expect(cli.locator(".connector-state b")).toHaveText(["Rechecking...", "Rechecking...", "Rechecking..."]);
  await expect(cli.locator(".connector-state").first().locator("small").last()).toHaveText("in the background");
  await expect(page.getByRole("button", { name: "Refresh this PC" })).toBeDisabled();
  await expect(cli.getByRole("button", { name: "Refresh gh" })).toBeDisabled();
  await expect(page.getByRole("status").first()).toHaveText("Rechecking in the background...");

  // Act: the check ends.
  supervisor.settle("machine", "", minutesBefore(0));
  await expect.poll(async () => { await page.clock.runFor(2000); return cli.locator(".connector-state b").first().textContent(); }).toBe("Verified");

  // Assert
  await expect(cli.locator(".connector-state").first().locator("small").last()).toHaveText("checked just now");
  await expect(page.getByRole("button", { name: "Refresh this PC" })).toBeEnabled();
  expect(supervisor.posts).toHaveLength(1);
});

test("a row refreshes its own project, and a goblin's row has no refresh of its own", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  await open(page, supervisor);
  await openProject(page, "globex");

  // Act
  await group("Services", page).getByRole("button", { name: "Refresh Supabase" }).click();

  // Assert
  expect(supervisor.posts).toEqual([{ path: "/api/connectors/refresh", token: "connectors-proof", body: { level: "project", scope: "globex" } }]);
  await expect(group("Services", page).locator(".connector-state b")).toHaveText("Rechecking...");

  // Act: a goblin.
  await crumbs(page).getByRole("button", { name: "Projects", exact: true }).click();
  await page.getByRole("button", { name: "acme", exact: true }).click();
  await page.getByRole("button", { name: ZEKE, exact: true }).click();

  // Assert
  await expect(group("Failed to load", page)).toBeVisible();
  await expect(page.locator(".connector-row .connector-refresh")).toHaveCount(0);
});

// A check is trusted for its level's life: a project's for an hour, this PC's
// and the account's for six, and a goblin is read from its record, so never.
test("a level past its life is marked Stale, and one inside it or never trusted for a life is not", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  const age = (level: { items: { checked_at?: unknown }[]; checked_at: string }, minutes: number) => { level.checked_at = minutesBefore(minutes); for (const each of level.items) each.checked_at = minutesBefore(minutes); };
  age(supervisor.view.machine, 361);
  age(supervisor.view.projects[0], 59);
  age(supervisor.view.projects[1], 61);
  age(supervisor.view.goblins[0], 100_000);

  // Act
  await open(page, supervisor);
  await tile(page, 1).click();

  // Assert: this PC and the account, six hours and a minute old.
  await expect(page.locator(".connector-row").filter({ has: page.locator(".connector-stale") })).toHaveCount(7);

  // Act
  await crumbs(page).getByRole("button", { name: "Connectors", exact: true }).click();
  await tile(page, 2).click();

  // Assert: a project of 61 minutes is Stale, one of 59 is not.
  await expect(page.locator(".connector-row").filter({ hasText: "globex" }).locator(".connector-stale")).toHaveText("Stale");
  await expect(page.locator(".connector-row").filter({ hasText: "acme" }).locator(".connector-stale")).toHaveCount(0);

  // Act
  await crumbs(page).getByRole("button", { name: "Connectors", exact: true }).click();
  await tile(page, 3).click();

  // Assert: a goblin read long ago is never Stale.
  await expect(page.locator(".connector-row")).toHaveCount(2);
  await expect(page.locator(".connector-stale")).toHaveCount(0);
});

test("a goblin whose server failed to load shows it on the home page, in the goblins list and in its own view, and a goblin that records nothing says so", async ({ page }) => {
  // Arrange
  await open(page, fleet());

  // Assert: home.
  await expect(group("Needs a look", page).getByRole("button", { name: "Acme Docs, " + ZEKE + " in acme" })).toBeVisible();

  // Act
  await tile(page, 3).click();

  // Assert: the list tells the goblin that failed from the one that did not.
  const goblins = group("Goblins", page);
  await expect(goblins.locator(".connector-open")).toHaveText([ZEKE, IVY]);
  await expect(goblins.locator(".connector-state b")).toHaveText(["1 server failed to load", "As its project"]);
  await expect(goblins.locator(".connector-state.is-trouble")).toHaveCount(1);

  // Act
  await goblins.getByRole("button", { name: ZEKE, exact: true }).click();

  // Assert: its own view names the server and the reason, from its record.
  const failed = group("Failed to load", page);
  await expect(failed.locator(".connector-name strong")).toHaveText("Acme Docs");
  await expect(failed.locator(".connector-state")).toContainText("Connection closed");
  await expect(failed.locator(".connector-sub")).toHaveText("read from its session record");
  await expect(failed.locator(".connector-state.is-trouble")).toHaveCount(1);

  // Act
  await crumbs(page).getByRole("button", { name: "Projects", exact: true }).click();
  await page.getByRole("button", { name: "globex", exact: true }).click();
  await page.getByRole("button", { name: IVY, exact: true }).click();

  // Assert
  await expect(page.getByText("Codex does not record which servers loaded", { exact: false })).toBeVisible();
  await expect(page.getByText("Nothing differs: it has what its project and this PC give every goblin.")).toBeVisible();
});

test("Connect gives a goblin a service and Disconnect takes it back, each posting the grant and then reading the snapshot again", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  await open(page, supervisor);
  await openGoblin(page, "acme", ZEKE);
  let release = () => {};
  supervisor.held = new Promise<void>((resolve) => { release = resolve; });
  const readsBefore = supervisor.reads;

  // Act
  await group("Not given", page).getByRole("button", { name: "Connect Supabase to " + ZEKE }).click();

  // Assert: the grant waits on the supervisor, and nothing else can be pressed meanwhile.
  await expect(group("Carries", page).getByRole("button", { name: "Disconnect GitHub from " + ZEKE })).toBeDisabled();
  expect(supervisor.posts).toEqual([{ path: "/api/connections/grant", token: "connectors-proof", body: { task: "cg-touch", generation: "cg-touch-1", connection: "service:supabase", action: "grant" } }]);
  release();
  await expect(group("Carries", page).getByRole("button", { name: "Disconnect Supabase from " + ZEKE })).toBeEnabled();
  await expect(group("Not given", page)).toHaveCount(0);
  expect(supervisor.reads).toBe(readsBefore + 1);

  // Act
  await group("Carries", page).getByRole("button", { name: "Disconnect GitHub from " + ZEKE }).click();

  // Assert
  await expect(group("Not given", page).getByRole("button", { name: "Connect GitHub to " + ZEKE })).toBeVisible();
  expect(supervisor.posts[1]).toEqual({ path: "/api/connections/grant", token: "connectors-proof", body: { task: "cg-touch", generation: "cg-touch-1", connection: "service:github", action: "revoke" } });
});

test("a Connect the supervisor refuses is said beside the page and leaves the service not given", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.refuses = "The goblin restarted.";
  await open(page, supervisor);
  await openGoblin(page, "acme", ZEKE);

  // Act
  await group("Not given", page).getByRole("button", { name: "Connect Supabase to " + ZEKE }).click();

  // Assert
  await expect(page.locator(".connectors-page > .notice")).toHaveText("The goblin restarted.");
  await expect(group("Not given", page).getByRole("button", { name: "Connect Supabase to " + ZEKE })).toBeEnabled();
});

test("a fix posts the project, the connection and the action to the connectors endpoint, shows the repair notice, and reads the snapshot again when its run ends", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.repaired = { message: "Store card is ready in the Command Center.", run_id: "fix-run-1" };
  await open(page, supervisor);
  await openProject(page, "globex");
  const readsBefore = supervisor.reads;

  // Act
  await page.getByRole("button", { name: "Store SUPABASE_KEY from clipboard" }).click();

  // Assert
  await expect(page).toHaveTitle("run:fix-run-1");
  expect(supervisor.posts).toEqual([{ path: "/api/connectors/fix", token: "connectors-proof", body: { project: "globex", connection: "service:supabase", action: "store:SUPABASE_KEY" } }]);
  const notice = page.locator(".connector-notice");
  await expect(notice).toContainText("Store card is ready in the Command Center.");
  await expect(notice.getByRole("button", { name: "Open repair card" })).toBeVisible();
  await expect(notice.getByRole("link", { name: "Open sign-in" })).toHaveCount(0);
  await page.evaluate(() => { document.title = ""; });
  await notice.getByRole("button", { name: "Open repair card" }).click();
  await expect(page).toHaveTitle("run:fix-run-1");
  const afterFix = supervisor.reads;
  expect(afterFix).toBe(readsBefore + 1);

  // Act: the repair's run ends, which the board's snapshot says.
  await page.evaluate(() => (window as unknown as { finishRepair: (id: string) => void }).finishRepair("fix-run-1"));

  // Assert
  await expect.poll(() => supervisor.reads).toBe(afterFix + 1);
});

test("a sign-in repair opens its page, a repair the supervisor refuses is said in a few words, and coming back rechecks that project once for each sign-in", async ({ page, context }) => {
  // Arrange
  await context.route("https://example.invalid/**", (route) => route.fulfill({ body: "Scratch sign-in", contentType: "text/html" }));
  const supervisor = fleet();
  supervisor.refuses = "The project changed.";
  await open(page, supervisor);
  await openProject(page, "globex");
  const signIn = page.getByRole("button", { name: "Sign in to Supabase" });
  const rechecks = () => supervisor.posts.filter((post) => post.path === "/api/connectors/refresh");
  const returnToBoard = () => page.evaluate(() => { window.dispatchEvent(new Event("focus")); window.dispatchEvent(new Event("focus")); });

  // Act: refused.
  await signIn.click();

  // Assert
  await expect(page.locator(".connectors-page > .notice")).toHaveText("The project changed.");
  await returnToBoard();
  expect(rechecks()).toEqual([]);

  // Act: allowed, and the Overlord signs in and returns twice.
  supervisor.refuses = "";
  supervisor.repaired = { url: "https://example.invalid/login", message: "Complete sign-in, then come back." };
  const opened = page.waitForEvent("popup");
  await signIn.click();
  const popup = await opened;
  await expect(popup).toHaveURL("https://example.invalid/login");
  await popup.close();
  await returnToBoard();

  // Assert
  await expect(page.getByRole("link", { name: "Open sign-in" })).toHaveAttribute("rel", "noreferrer");
  await expect.poll(() => rechecks().length).toBe(1);
  expect(rechecks()[0]).toEqual({ path: "/api/connectors/refresh", token: "connectors-proof", body: { level: "project", scope: "globex" } });
  await returnToBoard();
  expect(rechecks()).toHaveLength(1);
});

test("a level being checked is read every 2 seconds until it ends, and then the page asks nothing more", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.view.projects[0].checking = true;
  await open(page, supervisor);
  await tile(page, 2).click();
  await expect(page.locator(".connector-row").filter({ hasText: "acme" }).locator(".connector-state b")).toHaveText("Rechecking...");

  // Act: it is read again while it is checked.
  await expect.poll(async () => { await page.clock.runFor(2000); return supervisor.reads; }).toBeGreaterThanOrEqual(3);

  // Act: the check ends.
  supervisor.settle("project", "acme", minutesBefore(0));
  await expect.poll(async () => { await page.clock.runFor(2000); return page.locator(".connector-row").filter({ hasText: "acme" }).locator(".connector-state b").textContent(); }).toBe("Checked");

  // Assert
  const settled = supervisor.reads;
  await page.clock.runFor(10 * 60_000);
  expect(supervisor.reads).toBe(settled);
});

test("a check that never ends is read 30 more times and then left, and the page says it is still checking", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.view.machine.checking = true;

  // Act
  await open(page, supervisor);
  await expect.poll(async () => { await page.clock.runFor(2000); return supervisor.reads; }, { intervals: [50], timeout: 25_000 }).toBe(31);

  // Assert
  await expect(page.getByRole("status").first()).toHaveText("Still checking.");
  await page.clock.runFor(10 * 60_000);
  expect(supervisor.reads).toBe(31);

  // Act: reading again by hand reads once more, and the check has ended.
  supervisor.view.machine.checking = false;
  await page.getByRole("button", { name: "Read again" }).click();

  // Assert
  await expect.poll(() => supervisor.reads).toBe(32);
  await expect(page.getByRole("status").first()).toHaveText(/^Saved snapshot, oldest check/);
});

test("when the board's goblins change the snapshot is read again, and a snapshot that cannot be read is named to the CEO once and says so without an alert", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  await open(page, supervisor);
  expect(supervisor.reads).toBe(1);

  // Act: every goblin restarts on a new session.
  await page.evaluate(() => (window as unknown as { restartGoblins: () => void }).restartGoblins());

  // Assert
  await expect.poll(() => supervisor.reads).toBe(2);

  // Arrange
  const broken = fleet();
  broken.isUnreadable = true;
  const second = await page.context().newPage();
  await play(second, broken);

  // Act
  await second.goto("/tests/fixtures/connectors-page.html");

  // Assert
  await expect(second.getByText("Connectors unavailable")).toBeVisible();
  await expect.poll(() => broken.reports.map((report) => report.where)).toEqual(["reading the connectors"]);
  await expect(second.getByRole("alert")).toHaveCount(0);
  expect(broken.reads).toBe(1);
});

test("rows that open are buttons the keyboard reaches with Tab, and going back puts the focus on the row it came from", async ({ page }) => {
  // Arrange
  await open(page, fleet());
  await tile(page, 2).focus();

  // Act: Enter on a tile opens its level, and Tab reaches the first row.
  await page.keyboard.press("Enter");
  await expect(view(page, "Projects")).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "acme", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");

  // Assert: the new view takes the focus, and its first stop is its refresh.
  await expect(view(page, "acme")).toBeFocused();

  // Act: back up by the breadcrumb with the keyboard.
  await crumbs(page).getByRole("button", { name: "Projects", exact: true }).focus();
  await page.keyboard.press("Enter");

  // Assert
  await expect(page.getByRole("button", { name: "acme", exact: true })).toBeFocused();
});

const own = { id: "stripe-acme", provider: "Stripe", account: "Acme", projects: ["acme"], signed_in: true, signed_in_at: minutesBefore(120), used_at: minutesBefore(5), signs_out_after_fetch: false, in_use: false };
const shared = { id: "stripe-globex", provider: "Stripe", account: "Globex", projects: ["globex", "acme"], signed_in: false, used_at: minutesBefore(72 * 60), signs_out_after_fetch: true, in_use: false };

for (const viewport of [{ width: 1280, height: 1400 }, { width: 390, height: 844 }]) {
  test("a project lists its browser logins, signs out of one and forgets another only once asked, at width " + viewport.width, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize(viewport);
    const supervisor = fleet();
    supervisor.view.projects[0].browser_logins = [own, shared];
    await open(page, supervisor);
    await openProject(page, "acme");

    // Assert
    const list = page.getByRole("list", { name: "Browser logins" });
    await expect(list.getByRole("listitem")).toHaveCount(2);
    const acme = list.getByRole("listitem").filter({ hasText: "Stripe · Acme" });
    const globex = list.getByRole("listitem").filter({ hasText: "Stripe · Globex" });
    await expect(acme.locator(".connector-logo img")).toHaveAttribute("src", /^\/assets\/stripe-[\w-]{8}\.svg$/);
    await expect(acme.getByText("Signed in", { exact: true })).toBeVisible();
    await expect(acme.getByText("For acme. You signed in 2 hours ago, last used 5 minutes ago.", { exact: true })).toBeVisible();
    await expect(globex.getByText("Signed out", { exact: true })).toBeVisible();
    await expect(globex.getByText("For globex and acme. It signs out after each fetch, so each fetch brings the sign-in card. Last used 3 days ago.", { exact: true })).toBeVisible();
    await expect(globex.getByRole("button", { name: "Sign out of Stripe · Globex" })).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("browser-logins-" + viewport.width + ".png") });

    // Act
    await acme.getByRole("button", { name: "Sign out of Stripe · Acme" }).click();

    // Assert: it acts through the project's goblin.
    await expect(acme.getByText("Signed out", { exact: true })).toBeVisible();
    expect(supervisor.posts).toEqual([{ path: "/api/browser-logins/sign-out", token: "connectors-proof", body: { task: "cg-touch", generation: "cg-touch-1", login: "stripe-acme" } }]);

    // Act: Forget asks first, and Cancel leaves the login as it is.
    const forget = globex.getByRole("button", { name: "Forget Stripe · Globex" });
    await forget.click();
    const dialog = page.getByRole("dialog", { name: "Forget this login?" });

    // Assert
    await expect(dialog).toContainText("Stripe · Globex. Its browser profile is deleted, so the next fetch for globex and acme asks you to sign in again. Your Stripe account and its keys are not touched.");
    await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(forget).toBeFocused();
    expect(supervisor.posts).toHaveLength(1);

    // Act
    await forget.click();
    await page.getByRole("dialog", { name: "Forget this login?" }).getByRole("button", { name: "Forget" }).click();

    // Assert
    await expect(globex).toHaveCount(0);
    expect(supervisor.posts[1]).toEqual({ path: "/api/browser-logins/forget", token: "connectors-proof", body: { task: "cg-touch", generation: "cg-touch-1", login: "stripe-globex" } });
  });
}

test("the question to forget a login goes when the goblin it would act through restarts, and nothing is forgotten", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.view.projects[0].browser_logins = [own];
  await open(page, supervisor);
  await openProject(page, "acme");
  await page.getByRole("button", { name: "Forget Stripe · Acme" }).click();
  const dialog = page.getByRole("dialog", { name: "Forget this login?" });
  await expect(dialog).toBeVisible();

  // Act: the goblin restarts on a new session.
  supervisor.view.goblins[0].generation = "cg-touch-2";
  await page.evaluate(() => (window as unknown as { restartGoblins: () => void }).restartGoblins());

  // Assert
  await expect(dialog).toHaveCount(0);
  await expect(page.locator("dialog:modal")).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Browser logins" }).getByRole("listitem")).toHaveCount(1);
  expect(supervisor.posts).toEqual([]);
});

test("a login whose browser is in use waits for it to close, and a project with no goblin running cannot act on its logins", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.view.projects[0].browser_logins = [{ ...own, in_use: true }];
  supervisor.view.projects.push({ name: "initech", path: "C:/work/initech", browser_logins: [own], items: [], checked_at: minutesBefore(4), checking: false });
  await open(page, supervisor);

  // Act
  await openProject(page, "acme");

  // Assert
  const login = page.getByRole("list", { name: "Browser logins" }).getByRole("listitem");
  await expect(login).toContainText("In use by a fetch, a goblin or a sign-in window");
  await expect(login.getByRole("button", { name: "Sign out of Stripe · Acme" })).toBeDisabled();
  await expect(login.getByRole("button", { name: "Forget Stripe · Acme" })).toBeDisabled();

  // Act
  await crumbs(page).getByRole("button", { name: "Projects", exact: true }).click();
  await page.getByRole("button", { name: "initech", exact: true }).click();

  // Assert
  await expect(page.getByText("Sign out and Forget need a goblin running in this project.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Forget Stripe · Acme" })).toBeDisabled();
});

// The Overlord's page was slow because it checked every goblin on open. The
// first view of a fleet the size of today's, 22 goblins in 22 projects and
// about 400 items, draws from the saved snapshot in under a second, measured
// in the page from the start of the navigation to the first row on screen.
test("the first view of a snapshot the size of today's fleet draws its first row in under a second from the start of the navigation", async ({ page }, testInfo) => {
  // Arrange
  const supervisor = bigFleet();
  const items = supervisor.view.machine.items.length + supervisor.view.projects.reduce((sum, project) => sum + project.items.length, 0) + supervisor.view.goblins.reduce((sum, goblin) => sum + goblin.items.length, 0);
  expect(supervisor.view.goblins).toHaveLength(22);
  expect(supervisor.view.projects).toHaveLength(22);
  expect(items).toBeGreaterThanOrEqual(400);
  await play(page, supervisor);
  await page.addInitScript(() => {
    new MutationObserver((_, observer) => {
      if (!document.querySelector(".connector-row")) return;
      (window as unknown as { firstRowAt: number }).firstRowAt = performance.now();
      observer.disconnect();
    }).observe(document, { childList: true, subtree: true });
  });
  const goblins: Goblins = supervisor.view.goblins.map((goblin, at) => [goblin.task, "Goblin " + (at + 1), "Fleet Member"]);

  // Act
  await page.goto("/tests/fixtures/connectors-page.html?goblins=" + encodeURIComponent(JSON.stringify(goblins)));
  await expect(page.locator(".connector-row").first()).toBeVisible();
  const firstRowAt = await page.evaluate(() => (window as unknown as { firstRowAt: number }).firstRowAt);

  // Assert
  console.log(`first view: ${firstRowAt.toFixed(0)} ms from the start of the navigation to the first row, for ${items} items`);
  testInfo.annotations.push({ type: "first view", description: `${firstRowAt.toFixed(0)} ms for ${items} items` });
  expect(firstRowAt).toBeLessThan(1000);
  expect(supervisor.requests).toEqual(["GET /api/connectors"]);
});
