import { CHECKED_AT, play, supervisorWith, type Supervisor } from "./connectors-supervisor";
import { expect, test, type Page } from "./site";

// What the Connectors page asks the supervisor and when, and every repair
// the panel it replaced offered (#2303): it reads two goblins at a time,
// each until its check ends, and then nothing; a repair's card, its sign-in
// and its recheck; a check that failed; and a project's browser logins. The
// page stands alone here in the board's canvas, and the supervisor is played.
const PROOF = "connection-proof", NAME = "Build review panel";
const CONTEXT7 = { id: "mcp:context7", name: "Context7", kind: "mcp", status: "unauthorized", detail: "Sign-in required.", checked_at: CHECKED_AT, actions: ["login"] };
const GITHUB = { id: "mcp:github", name: "GitHub", kind: "mcp", status: "probe_succeeded", scope: "separate_probe", source: "Codex", checked_at: CHECKED_AT };

async function open(page: Page, supervisor: Supervisor, goblins = 1) {
  await play(page, supervisor);
  await page.goto("/tests/fixtures/connectors-page.html?goblins=" + goblins);
}

// Opens a connector's own page from the list.
async function openConnector(page: Page, name: string) {
  await page.locator(".connector-row").getByRole("button", { name, exact: true }).click();
  return page.getByRole("article", { name });
}

test("the page reads two goblins at a time, each until its check has ended, and then asks nothing more", async ({ page }) => {
  // Arrange
  await page.clock.install();
  const second = PROOF + "-2", third = PROOF + "-3";
  const supervisor = supervisorWith({ [PROOF]: [GITHUB], [second]: [GITHUB], [third]: [GITHUB] });
  supervisor.checking = { [PROOF]: true, [second]: true, [third]: true };

  // Act
  await open(page, supervisor, 3);
  await expect(page.getByText("Checking connectors...", { exact: true })).toBeVisible();

  // Assert: the first two are read again and again while their checks run,
  // and the third waits for one of them to end.
  await expect.poll(async () => { await page.clock.runFor(2000); return Math.min(supervisor.reads[PROOF] ?? 0, supervisor.reads[second] ?? 0); }).toBeGreaterThan(2);
  expect(supervisor.reads[third] ?? 0).toBe(0);
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeDisabled();

  // Act: the first goblin's check ends, which gives the third its turn.
  supervisor.checking[PROOF] = false;

  // Assert
  await expect.poll(async () => { await page.clock.runFor(2000); return supervisor.reads[third] ?? 0; }).toBeGreaterThan(0);

  // Act: every check ends.
  supervisor.checking = {};

  // Assert: the page says so, and a page left open asks nothing.
  await expect.poll(async () => { await page.clock.runFor(2000); return page.getByText("Checking connectors...", { exact: true }).count(); }).toBe(0);
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeEnabled();
  const settled = { ...supervisor.reads };
  await page.clock.runFor(5 * 60 * 1000);
  expect(supervisor.reads).toEqual(settled);
});

test("a failing connector's repair opens its Command Center card, and the goblin is read again only when the repair ends", async ({ page }) => {
  // Arrange
  await page.clock.install();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const supervisor = supervisorWith({ [PROOF]: [GITHUB, CONTEXT7] });
  supervisor.repaired = { message: "Login card is ready in the Command Center.", run_id: "connections-login-fixture" };
  await open(page, supervisor);
  await expect(page.locator(".connector-row").filter({ hasText: "Context7" }).locator(".connector-state.is-trouble")).toHaveText("Sign in");
  const detail = await openConnector(page, "Context7");
  const goblin = detail.getByRole("listitem").filter({ hasText: NAME });
  await expect(goblin.getByText("Sign in: Sign-in required.", { exact: true })).toBeVisible();

  // Act
  await goblin.getByRole("button", { name: "Sign in to Context7 for " + NAME, exact: true }).click();

  // Assert: the repair is asked of the supervisor for that goblin's connector.
  await expect(page).toHaveTitle("run:connections-login-fixture");
  expect(supervisor.posts).toEqual([{ path: "/api/connections/fix", token: "connections-proof", body: { task: PROOF, generation: "first", connection: "mcp:context7", action: "login" } }]);
  await expect(page.locator(".connector-notice")).toContainText("Login card is ready in the Command Center.");
  await expect(page.locator(".connector-notice").getByRole("button", { name: "Open repair card" })).toBeVisible();
  await page.clock.runFor(5 * 60 * 1000);
  await expect(goblin.getByText("Sign in: Sign-in required.", { exact: true })).toBeVisible();
  expect(supervisor.reads).toEqual({ [PROOF]: 1 });

  // Act: the repair ends.
  supervisor.entries[PROOF] = [GITHUB, { ...CONTEXT7, status: "connected", detail: "", actions: [] }];
  await page.evaluate(() => (window as unknown as { finishRepair: (id: string) => void }).finishRepair("connections-login-fixture"));

  // Assert
  await expect(goblin).toContainText("Connected");
  await expect(goblin.getByRole("button")).toHaveCount(0);
  expect(supervisor.reads).toEqual({ [PROOF]: 2 });
  expect(errors).toEqual([]);
});

// The Overlord, 2026-10-08: "everything error wise goes to cfo". A check that
// timed out says so in two words where the page says what it is doing.
test("a check that timed out says Check failed, without the supervisor's words and without an alert", async ({ page }) => {
  // Arrange
  const supervisor = supervisorWith({ [PROOF]: [] });
  supervisor.error = "Connection check timed out.";

  // Act
  await open(page, supervisor);

  // Assert
  await expect(page.getByText("Check failed", { exact: true })).toBeVisible();
  await expect(page.getByText("Connection check timed out.")).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeEnabled();
});

test("OAuth opens its sign-in, and a repair the supervisor refuses is said in a few words beside it", async ({ page, context }) => {
  // Arrange
  await context.route("https://example.invalid/**", (route) => route.fulfill({ body: "Scratch sign-in", contentType: "text/html" }));
  const supervisor = supervisorWith({ [PROOF]: [CONTEXT7] });
  supervisor.refuses = "Task restarted. Refresh connections.";
  await open(page, supervisor);
  const detail = await openConnector(page, "Context7");
  const signIn = detail.getByRole("button", { name: "Sign in to Context7 for " + NAME, exact: true });

  // Act
  await signIn.click();

  // Assert
  await expect(page.locator(".connectors-page > .notice")).toHaveText("Task restarted. Refresh connections.");

  // Act
  supervisor.refuses = "";
  supervisor.repaired = { url: "https://example.invalid/login", message: "Complete sign-in, then recheck the connection." };
  const opened = page.waitForEvent("popup");
  await signIn.click();

  // Assert
  const popup = await opened;
  await expect(popup).toHaveURL("https://example.invalid/login");
  await expect(page.getByRole("link", { name: "Open sign-in" })).toHaveAttribute("rel", "noreferrer");
  await popup.close();
});

test("returning from a sign-in rechecks once for each sign-in", async ({ page, context }) => {
  // Arrange
  await context.route("https://example.invalid/**", (route) => route.fulfill({ body: "Scratch sign-in", contentType: "text/html" }));
  const supervisor = supervisorWith({ [PROOF]: [CONTEXT7] });
  supervisor.repaired = { url: "https://example.invalid/login", message: "Complete sign-in, then recheck the connection." };
  const rechecks = () => supervisor.posts.filter((post) => post.path === "/api/connections/check").length;
  const returnToBoard = () => page.evaluate(() => { window.dispatchEvent(new Event("focus")); window.dispatchEvent(new Event("focus")); });
  await open(page, supervisor);
  const detail = await openConnector(page, "Context7");
  await returnToBoard();

  // Act
  const first = page.waitForEvent("popup");
  await detail.getByRole("button", { name: "Sign in to Context7 for " + NAME, exact: true }).click();
  await (await first).close();
  await returnToBoard();

  // Assert
  await expect.poll(rechecks).toBe(1);
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeEnabled();

  // Act
  await returnToBoard();
  await returnToBoard();
  const second = page.waitForEvent("popup");
  await page.getByRole("link", { name: "Open sign-in" }).click();
  await (await second).close();
  await returnToBoard();

  // Assert
  await expect.poll(rechecks).toBe(2);
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeEnabled();
  expect(rechecks()).toBe(2);
  expect(supervisor.posts.filter((post) => post.path === "/api/connections/check").map((post) => post.token)).toEqual(["connections-proof", "connections-proof"]);
});

test("Recheck has the supervisor check every goblin anew", async ({ page }) => {
  // Arrange
  const supervisor = supervisorWith({ [PROOF]: [GITHUB], [PROOF + "-2"]: [GITHUB] });
  await open(page, supervisor, 2);
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeEnabled();

  // Act
  await page.getByRole("button", { name: "Recheck connectors" }).click();

  // Assert
  await expect.poll(() => supervisor.posts).toEqual([
    { path: "/api/connections/check", token: "connections-proof", body: { task: PROOF, generation: "first" } },
    { path: "/api/connections/check", token: "connections-proof", body: { task: PROOF + "-2", generation: "first" } },
  ]);
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeEnabled();
});

// The connections that could not be read are named to the CFO by goblin and
// task: "a goblin's connections" told it nothing it could act on.
test("connections that cannot be read are named to the CFO by goblin and task, once, and the page says the check failed", async ({ page }) => {
  // Arrange
  const supervisor = supervisorWith({});
  supervisor.isUnreadable = true;

  // Act
  await open(page, supervisor);

  // Assert
  await expect.poll(() => supervisor.reports.map((report) => report.where)).toEqual(["reading the connections of " + NAME + " (" + PROOF + ")"]);
  await expect(page.getByText("Check failed", { exact: true })).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(supervisor.reads).toEqual({ [PROOF]: 1 });
});

const now = Date.parse("2026-10-13T20:00:00Z");
const hoursAgo = (hours: number) => new Date(now - hours * 3_600_000).toISOString();
const own = { id: "stripe-precisiondocs", provider: "Stripe", account: "PrecisionDocs", projects: ["code-goblins"], signed_in: true, signed_in_at: hoursAgo(2), used_at: hoursAgo(5 / 60), signs_out_after_fetch: false, in_use: false };
const shared = { id: "stripe-siqstack", provider: "Stripe", account: "SIQstack", projects: ["SIQshift", "code-goblins"], signed_in: false, used_at: hoursAgo(72), signs_out_after_fetch: true, in_use: false };

for (const viewport of [{ width: 1280, height: 1400 }, { width: 390, height: 844 }]) {
  test("the page lists a project's browser logins once, signs out of one and forgets another only once asked, at width " + viewport.width, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize(viewport);
    await page.clock.install({ time: now });
    const supervisor = supervisorWith({ [PROOF]: [], [PROOF + "-2"]: [] });
    supervisor.logins = [own, shared];
    await open(page, supervisor, 2);

    // Act
    await page.getByRole("group", { name: "Connectors shown" }).getByRole("button", { name: /^Browser logins/ }).click();

    // Assert
    const list = page.getByRole("list", { name: "Browser logins" });
    await expect(list.getByRole("listitem")).toHaveCount(2);
    const precision = list.getByRole("listitem").filter({ hasText: "Stripe · PrecisionDocs" });
    const siqstack = list.getByRole("listitem").filter({ hasText: "Stripe · SIQstack" });
    await expect(precision.locator(".connector-logo img")).toHaveAttribute("src", "/assets/connectors/stripe.svg");
    await expect(precision.getByText("Signed in", { exact: true })).toBeVisible();
    await expect(precision.getByText("For code-goblins. You signed in 2 hours ago, last used 5 minutes ago.", { exact: true })).toBeVisible();
    await expect(siqstack.getByText("Signed out", { exact: true })).toBeVisible();
    await expect(siqstack.getByText("For SIQshift and code-goblins. It signs out after each fetch, so each fetch brings the sign-in card. Last used 3 days ago.", { exact: true })).toBeVisible();
    await expect(siqstack.getByRole("button", { name: "Sign out of Stripe · SIQstack" })).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    // Each login's name shows whole, so two logins of one provider are told
    // apart at every width: its signed-in state goes below it when both do
    // not fit on one line.
    for (const name of ["Stripe · PrecisionDocs", "Stripe · SIQstack"]) {
      const isWhole = await list.getByText(name, { exact: true }).evaluate((element) => element.scrollWidth <= element.clientWidth);
      expect(isWhole, name + " shows whole").toBe(true);
    }
    await page.screenshot({ path: testInfo.outputPath("browser-logins-" + viewport.width + ".png") });

    // Act
    await precision.getByRole("button", { name: "Sign out of Stripe · PrecisionDocs" }).click();

    // Assert
    await expect(precision.getByText("Signed out", { exact: true })).toBeVisible();
    expect(supervisor.posts).toEqual([{ path: "/api/browser-logins/sign-out", token: "connections-proof", body: { task: PROOF, generation: "first", login: "stripe-precisiondocs" } }]);

    // Act: Forget asks first, and Cancel leaves the login as it is.
    const forget = siqstack.getByRole("button", { name: "Forget Stripe · SIQstack" });
    await forget.click();
    const dialog = page.getByRole("dialog", { name: "Forget this login?" });

    // Assert: the question holds the keyboard, and Cancel hands it back.
    await expect(dialog).toContainText("Stripe · SIQstack. Its browser profile is deleted, so the next fetch for SIQshift and code-goblins asks you to sign in again. Your Stripe account and its keys are not touched.");
    await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
    expect(await dialog.evaluate((element) => element.matches(":modal"))).toBe(true);
    await page.keyboard.press("Tab");
    await expect(dialog.getByRole("button", { name: "Forget" })).toBeFocused();
    await page.keyboard.press("Shift+Tab");
    await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(forget).toBeFocused();
    expect(supervisor.posts).toHaveLength(1);

    // Act: Escape answers as Cancel does.
    await forget.click();
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Escape");

    // Assert
    await expect(dialog).toHaveCount(0);
    await expect(forget).toBeFocused();
    await expect(siqstack).toBeVisible();
    expect(supervisor.posts).toHaveLength(1);

    // Act
    await forget.click();
    await page.getByRole("dialog", { name: "Forget this login?" }).getByRole("button", { name: "Forget" }).click();

    // Assert
    await expect(siqstack).toHaveCount(0);
    expect(supervisor.posts[1]).toEqual({ path: "/api/browser-logins/forget", token: "connections-proof", body: { task: PROOF, generation: "first", login: "stripe-siqstack" } });
  });
}

test("the question to forget a login goes when the goblin it would act through restarts, and nothing is forgotten", async ({ page }) => {
  // Arrange
  await page.clock.install({ time: now });
  const supervisor = supervisorWith({ [PROOF]: [] });
  supervisor.logins = [own];
  await open(page, supervisor);
  await page.getByRole("group", { name: "Connectors shown" }).getByRole("button", { name: /^Browser logins/ }).click();
  await page.getByRole("button", { name: "Forget Stripe · PrecisionDocs" }).click();
  const dialog = page.getByRole("dialog", { name: "Forget this login?" });
  await expect(dialog).toBeVisible();
  const mounted = await dialog.elementHandle();

  // Act: the goblin restarts on a new session.
  await page.evaluate(() => (window as unknown as { restartGoblins: () => void }).restartGoblins());

  // Assert: the question is gone for good, though the new session lists the login again.
  await expect(dialog).toHaveCount(0);
  expect(await mounted!.evaluate((element) => element.isConnected)).toBe(false);
  await expect(page.getByRole("list", { name: "Browser logins" }).getByRole("listitem")).toHaveCount(1);
  await expect(page.locator("dialog:modal")).toHaveCount(0);
  expect(supervisor.posts).toEqual([]);
});

test("a login whose browser is in use waits for it to close before it signs out or is forgotten", async ({ page }) => {
  // Arrange
  await page.clock.install({ time: now });
  const supervisor = supervisorWith({ [PROOF]: [] });
  supervisor.logins = [{ ...own, in_use: true }];
  await open(page, supervisor);

  // Act
  await page.getByRole("group", { name: "Connectors shown" }).getByRole("button", { name: /^Browser logins/ }).click();

  // Assert
  const login = page.getByRole("list", { name: "Browser logins" }).getByRole("listitem");
  await expect(login).toContainText("In use by a fetch, a goblin or a sign-in window");
  await expect(login.getByRole("button", { name: "Sign out of Stripe · PrecisionDocs" })).toBeDisabled();
  await expect(login.getByRole("button", { name: "Forget Stripe · PrecisionDocs" })).toBeDisabled();
});
