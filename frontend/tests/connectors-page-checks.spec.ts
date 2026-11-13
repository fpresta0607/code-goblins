import { CHECKED_AT, play, supervisorWith, type Supervisor } from "./connectors-supervisor";
import { expect, test, type Locator, type Page } from "./site";

// What the Connectors page asks the supervisor and when, and every repair
// the panel it replaced offered (#2303): it reads every goblin at once,
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

// #2394: every open rechecked each goblin, two at a time with the rest
// waiting their turn, and the page said Checking connectors... for minutes
// on each one. Reopened, it reads every goblin at once: each one's last
// result shows at once with how old it is while its recheck runs behind it,
// and then it asks nothing more.
test("reopened, the page shows every goblin's last connectors at once with their age while their rechecks run, and then asks nothing more", async ({ page }) => {
  // Arrange: six goblins, each with a server of its own, last checked twelve
  // minutes ago but the fourth, checked twenty, and each rechecked now.
  const checked = Date.parse(CHECKED_AT);
  await page.clock.install({ time: checked + 12 * 60_000 });
  const servers = ["alpha", "bravo", "charlie", "delta", "echo", "foxtrot"];
  const ids = servers.map((_, at) => at ? PROOF + "-" + (at + 1) : PROOF);
  const supervisor = supervisorWith(Object.fromEntries(ids.map((id, at) => [id, [{ ...GITHUB, id: "mcp:" + servers[at], name: servers[at] }]])));
  supervisor.checking = Object.fromEntries(ids.map((id) => [id, true]));
  supervisor.checkedAtByTask = { [ids[3]]: new Date(checked - 8 * 60_000).toISOString() };

  // Act
  await open(page, supervisor, ids.length);

  // Assert: every goblin is read, its server listed, and the page says how
  // old the oldest result is while it is rechecked, not that it is checking.
  await expect(page.locator(".connector-row .connector-open")).toHaveText(["Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot"]);
  await expect(page.locator(".connectors-head [role=status]")).toHaveText("Checked 20 minutes ago, rechecking...");
  expect(ids.filter((id) => !supervisor.reads[id])).toEqual([]);
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeDisabled();

  // Act: every recheck ends.
  supervisor.checking = {};
  supervisor.checkedAt = new Date(checked + 12 * 60_000).toISOString();
  supervisor.checkedAtByTask = {};

  // Assert: the page says so, and a page left open asks nothing.
  await expect.poll(async () => { await page.clock.runFor(2000); return page.locator(".connectors-head [role=status]").textContent(); }).toBe("Checked just now");
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeEnabled();
  await expect(page.getByText("Checking connectors...", { exact: true })).toHaveCount(0);
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
  // The list's Sign in is a button that opens the connector's page, where
  // the goblin that must sign in has its own Sign in beside its reason.
  await page.locator(".connector-row").getByRole("button", { name: "Sign in to Context7", exact: true }).click();
  const detail = page.getByRole("article", { name: "Context7" });
  const goblin = detail.getByRole("listitem").filter({ hasText: NAME });
  await expect(goblin.locator(".connector-use")).toHaveText("Sign-in required.");
  await expect(goblin.locator(".connector-word")).toHaveCount(0);

  // Act
  await goblin.getByRole("button", { name: "Sign in to Context7 for " + NAME, exact: true }).click();

  // Assert: the repair is asked of the supervisor for that goblin's connector.
  await expect(page).toHaveTitle("run:connections-login-fixture");
  expect(supervisor.posts).toEqual([{ path: "/api/connections/fix", token: "connections-proof", body: { task: PROOF, generation: "first", connection: "mcp:context7", action: "login" } }]);
  await expect(page.locator(".connector-notice")).toContainText("Login card is ready in the Command Center.");
  await expect(page.locator(".connector-notice").getByRole("button", { name: "Open repair card" })).toBeVisible();
  await page.clock.runFor(5 * 60 * 1000);
  await expect(goblin.locator(".connector-use")).toHaveText("Sign-in required.");
  expect(supervisor.reads).toEqual({ [PROOF]: 1 });

  // Act: the repair ends.
  supervisor.entries[PROOF] = [GITHUB, { ...CONTEXT7, status: "connected", detail: "", actions: [] }];
  await page.evaluate(() => (window as unknown as { finishRepair: (id: string) => void }).finishRepair("connections-login-fixture"));

  // Assert
  await expect(goblin.locator(".connector-word")).toHaveText("Verified");
  await expect(goblin.getByRole("button")).toHaveCount(0);
  expect(supervisor.reads).toEqual({ [PROOF]: 2 });
  expect(errors).toEqual([]);
});

// The Overlord, 2026-11-11: only a failed check is amber, with no warning
// mark, and only a sign-in the board can start is its green Sign in. A check
// that found a sign-in expired, and a sign-in only a stored variable repairs,
// each keep their word in amber in the list and beside the goblin, with the
// check's reason and the repair it offers.
for (const { name, entry, shown, word, reason, repair } of [
  { name: "a check that found its sign-in expired", entry: { id: "mcp:linear", name: "Linear", kind: "mcp", status: "expired", detail: "Sign-in expired.", checked_at: CHECKED_AT }, shown: "Linear", word: "Expired", reason: "Sign-in expired.", repair: "" },
  { name: "a sign-in only a stored variable repairs", entry: { id: "service:docs", name: "docs", kind: "service", status: "unauthorized", detail: "Sign-in required.", checked_at: CHECKED_AT, actions: ["store:DOCS_TOKEN"], grant: "revoke" }, shown: "Docs", word: "Sign in", reason: "Sign-in required.", repair: "Store DOCS_TOKEN from clipboard" },
]) {
  test(`${name} reads its word in amber with no warning mark, in the list and beside the goblin with its reason`, async ({ page }) => {
    // Arrange
    await open(page, supervisorWith({ [PROOF]: [GITHUB, entry] }));
    const colour = (locator: Locator) => locator.evaluate((element) => getComputedStyle(element).color);
    const amber = await page.evaluate(() => {
      const probe = document.body.appendChild(document.createElement("span"));
      probe.style.color = "var(--amber)";
      const found = getComputedStyle(probe).color;
      probe.remove();
      return found;
    });
    const row = page.locator(".connector-row").filter({ has: page.getByRole("button", { name: shown, exact: true }) });

    // Assert: the list says its word in amber, as no type is, and offers no Sign in.
    await expect(row.locator(".connector-state.is-trouble")).toHaveText(word);
    await expect(row.locator(".connector-status .icon")).toHaveCount(0);
    await expect(row.locator(".connector-status button")).toHaveCount(0);
    expect(await colour(row.locator(".connector-state"))).toBe(amber);
    expect(await colour(row.locator(".connector-type"))).not.toBe(amber);

    // Act
    const detail = await openConnector(page, shown);

    // Assert: beside the goblin, its word in amber with its reason, and only the repair it offers.
    const goblin = detail.getByRole("listitem").filter({ hasText: NAME });
    await expect(goblin.locator(".connector-use")).toHaveText(word + ". " + reason);
    await expect(goblin.locator(".connector-use .icon")).toHaveCount(0);
    expect(await colour(goblin.locator(".connector-word"))).toBe(amber);
    await expect(goblin.getByRole("button", { name: "Sign in to " + shown + " for " + NAME, exact: true })).toHaveCount(0);
    if (repair) await expect(goblin.getByRole("button", { name: repair + " for " + NAME, exact: true })).toBeVisible();
    else await expect(goblin.locator(".connector-goblin-actions button")).toHaveCount(0);
  });
}

// #2358: on a phone, Checking connectors... ran into the heading beside it,
// which kept its place but not its words. Each says what the page is doing
// on a line of its own under the heading, clear of the recheck button.
for (const isFailing of [false, true]) {
  test(`on a phone, ${isFailing ? "Check failed" : "Checking connectors..."} takes a line of its own, clear of the heading and the recheck button`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width: 390, height: 844 });
    const supervisor = supervisorWith({ [PROOF]: [GITHUB] });
    if (isFailing) supervisor.error = "Connection check timed out.";
    else supervisor.unchecked = { [PROOF]: true };

    // Act
    await open(page, supervisor);
    await expect(page.locator(".connectors-head [role=status]")).toHaveText(isFailing ? "Check failed" : "Checking connectors...");
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({ path: testInfo.outputPath(`connectors-status-390-${isFailing ? "failed" : "checking"}.png`), clip: { x: 0, y: 0, width: 390, height: 320 } });

    // Assert: the words of each part of the head, and the button, each in a
    // place of its own.
    const parts = await page.locator(".connectors-head").evaluate((head) => [...head.children].map((part) => {
      const words = document.createRange();
      words.selectNodeContents(part);
      const box = part.tagName === "BUTTON" ? part.getBoundingClientRect() : words.getBoundingClientRect();
      return { name: part.tagName, left: box.left, right: box.right, top: box.top, bottom: box.bottom, isWhole: part.scrollWidth <= part.clientWidth };
    }));
    const head = await page.locator(".connectors-head").boundingBox();
    for (const [at, part] of parts.entries()) {
      expect(part.isWhole, part.name + " shows whole").toBe(true);
      expect(part.left >= head!.x - 0.5 && part.right <= head!.x + head!.width + 0.5, part.name + " stays in the head").toBe(true);
      for (const other of parts.slice(at + 1)) {
        const isApart = part.right <= other.left || other.right <= part.left || part.bottom <= other.top || other.bottom <= part.top;
        expect(isApart, part.name + " and " + other.name + " do not overlap").toBe(true);
      }
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "the page does not scroll sideways").toBe(true);
  });
}

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

// A goblin whose last check failed keeps saying so while its recheck runs,
// and says it is rechecking, since a recheck can take minutes (#2394).
test("a check that failed says Check failed, rechecking... while its recheck runs, and Check failed once it ends failed again", async ({ page }) => {
  // Arrange
  await page.clock.install();
  const supervisor = supervisorWith({ [PROOF]: [GITHUB] });
  supervisor.error = "Connection check timed out.";
  supervisor.checking = { [PROOF]: true };
  const status = page.locator(".connectors-head [role=status]");

  // Act
  await open(page, supervisor);

  // Assert
  await expect(status).toHaveText("Check failed, rechecking...");
  await expect(page.getByRole("button", { name: "Recheck connectors" })).toBeDisabled();

  // Act: the recheck ends with the same error.
  supervisor.checking = {};

  // Assert
  await expect.poll(async () => { await page.clock.runFor(2000); return status.textContent(); }).toBe("Check failed");
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

// The connections that could not be read are named to the CEO by goblin and
// task: "a goblin's connections" told it nothing it could act on.
test("connections that cannot be read are named to the CEO by goblin and task, once, and the page says the check failed", async ({ page }) => {
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
    await expect(precision.locator(".connector-logo img")).toHaveAttribute("src", /^\/assets\/stripe-[\w-]{8}\.svg$/);
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
