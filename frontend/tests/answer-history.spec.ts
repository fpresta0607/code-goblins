import { expect, test, type Page, type Route } from "./site";

// The Overlord, 2026-10-11 about 01:00Z: "when i go back in command center it
// should show the selcted answer if you ewent ahead where you can alwasy slect
// a enw one and change as needed safely and smoothly indempotentlty". Every
// answered question in History opens again with its answer checked, who chose
// it and when, and takes a new answer at any time, whoever gave the first. The
// card shows his change on the frame he makes it, with no spinner and no error
// line, and a change goes once.
test.use({ locale: "en-US", timezoneId: "America/Chicago", viewport: { width: 1707, height: 1067 }, deviceScaleFactor: 1.5 });

const NOW = new Date("2026-10-11T01:30:00Z");

async function history(page: Page) {
  await page.clock.install({ time: NOW });
  await page.goto("/tests/fixtures/answer-history.html");
  await page.locator(".command-center-menu > summary").click();
  const panel = page.locator(".command-center-updates");
  await panel.getByText("History").click();
  return panel.locator(".inbox-history li");
}

// reopen opens the answered question asked by asker from History.
async function reopen(page: Page, asker: RegExp) {
  await history(page);
  await page.getByRole("button", { name: asker }).click();
  return page.getByRole("dialog");
}

// actions records what the board posts, and answers each with fulfill, which
// takes the request's body; by default the supervisor queues it.
async function actions(page: Page, fulfill = (route: Route, body: Record<string, unknown>) => route.fulfill({ status: 202, json: { ...body, status: "queued" } })) {
  const posted: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    posted.push(body);
    await fulfill(route, body);
  });
  return posted;
}

for (const width of [1707, 390]) {
  test(`History says who answered each question and offers every answer to change, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height: 1067 });

    // Act
    const rows = await history(page);

    // Assert
    await expect(rows).toHaveCount(4);
    const [gate, merge, accent, tenants] = [rows.nth(0), rows.nth(1), rows.nth(2), rows.nth(3)];
    await expect(gate.getByRole("img", { name: "The CFO answered", exact: true })).toBeVisible();
    await expect(gate.locator(".answer-reason")).toHaveText("Reason: the running test step ends in 4 minutes");
    await expect(merge.getByRole("img", { name: "You answered" })).toBeVisible();
    await expect(accent.getByRole("img", { name: "The CFO answered while you were away" })).toBeVisible();
    await expect(tenants.getByRole("img", { name: "The CFO answered while you were away" })).toBeVisible();
    await expect(rows.getByRole("button", { name: /^Change the answer to / })).toHaveCount(4);
    await expect(merge.getByRole("button", { name: "Change the answer to The CFO: Merge PocketPiggies 125? CI is green on current main." })).toHaveAttribute("data-tip", "Change answer");
    const overflow = await page.locator(".command-center-updates").evaluate((element) => element.scrollWidth - element.clientWidth);
    expect(overflow).toBe(0);
    await page.locator(".command-center-updates").screenshot({ path: testInfo.outputPath(`history-${width}.png`) });
  });
}

test("an answered question reopens with its answer checked, and who chose it and when", async ({ page }, testInfo) => {
  for (const [asker, chosen, who] of [
    [/^Change the answer to The CFO:/, "Merge it", "You chose this · 40m ago"],
    [/^Change the answer to Bernie - Gate Keeper:/, "Wait for the running test step to end", "The CFO chose this · 2m ago"],
    [/^Change the answer to Lucy - Pixel Polisher:/, "Mint", "The CFO chose this while you were away · 3h ago"],
  ] as const) {
    // Arrange
    const dialog = await reopen(page, asker);

    // Act
    const status = dialog.locator(".answered-by");

    // Assert
    await expect(dialog.getByRole("radio", { name: chosen })).toBeChecked();
    await expect(dialog.getByRole("radio", { checked: true })).toHaveCount(1);
    await expect(status).toHaveText(who);
    await expect(dialog.getByRole("button", { name: "Change answer" })).toBeDisabled();
    await expect(dialog.locator(".change-warning")).toHaveCount(0);
    await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath(`reopened-${chosen.split(" ")[0]}.png`) });
  }
});

test("he changes an answer and the card shows it on the frame he makes it, with no spinner and no error line", async ({ page }, testInfo) => {
  // Arrange
  const posted = await actions(page);
  const dialog = await reopen(page, /^Change the answer to The CFO:/);
  await dialog.getByRole("radio", { name: "Keep it open" }).check();

  // Act
  await dialog.getByRole("button", { name: "Change answer" }).click();

  // Assert
  await expect(dialog.getByRole("radio", { name: "Keep it open" })).toBeChecked();
  await expect(dialog.locator(".answered-by")).toHaveText("You chose this · 0s ago");
  await expect(dialog.getByRole("button", { name: "Change answer" })).toBeDisabled();
  await expect.poll(() => posted.length).toBe(1);
  expect(posted.map((body) => [body.kind, body.question_id, body.generation, body.text, body.answer_kind])).toEqual([["answer_change", "pp-merge-125-20261010", "f".repeat(64), "Keep it open", "option"]]);
  await expect(dialog.locator(".question-card")).not.toContainText(/Sending|Changing|did not reach|failed/);
  await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath("changed.png") });
  await page.evaluate((id) => window.changeLands?.(id, "pp-merge-125-20261010", "Keep it open"), String(posted[0].id));
  await expect(dialog.getByRole("radio", { name: "Keep it open" })).toBeChecked();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await page.locator(".command-center-menu > summary").click();
  await expect(page.locator(".inbox-history li").filter({ hasText: "PocketPiggies" }).locator(".answer-reason")).toHaveText("Changed from Merge it");
});

test("he writes his own answer in place of the one chosen", async ({ page }) => {
  // Arrange
  const posted = await actions(page);
  const dialog = await reopen(page, /^Change the answer to Bernie - Gate Keeper:/);
  await dialog.getByRole("radio", { name: /^Other/ }).check();
  await dialog.getByRole("textbox", { name: "Your written answer" }).fill("Start it once the test step ends");

  // Act
  await dialog.getByRole("button", { name: "Change answer" }).click();

  // Assert
  await expect.poll(() => posted.length).toBe(1);
  expect(posted[0]).toMatchObject({ kind: "answer_change", question_id: "notify-cg-verify-fast-12", text: "Start it once the test step ends", answer_kind: "other" });
  await expect(dialog.locator(".answered-by")).toHaveText("You wrote this · 0s ago");
});

test("choosing the answer already chosen sends nothing", async ({ page }) => {
  // Arrange
  const posted = await actions(page);
  const dialog = await reopen(page, /^Change the answer to The CFO:/);
  await dialog.getByRole("radio", { name: "Keep it open" }).check();
  await dialog.getByRole("radio", { name: "Merge it" }).check();

  // Act
  await dialog.getByRole("radio", { name: "Merge it" }).press("Enter");
  await page.clock.runFor(2000);

  // Assert
  await expect(dialog.getByRole("button", { name: "Change answer" })).toBeDisabled();
  expect(posted).toHaveLength(0);
});

test("a double click sends one change", async ({ page }) => {
  // Arrange
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const posted = await actions(page, async (route, body) => { await held; await route.fulfill({ status: 202, json: { ...body, status: "queued" } }); });
  const dialog = await reopen(page, /^Change the answer to The CFO:/);
  await dialog.getByRole("radio", { name: "Keep it open" }).check();

  // Act
  await dialog.getByRole("button", { name: "Change answer" }).dblclick();
  await page.clock.runFor(2000);
  release();
  await page.clock.runFor(2000);

  // Assert
  expect(posted).toHaveLength(1);
  await expect(dialog.getByRole("radio", { name: "Keep it open" })).toBeChecked();
});

test("a change the dropped connection kept from the supervisor goes again under the same request ID, with the card unchanged", async ({ page }) => {
  // Arrange
  let attempts = 0;
  const posted = await actions(page, async (route, body) => {
    if (++attempts === 1) return route.abort("connectionreset");
    await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  });
  const dialog = await reopen(page, /^Change the answer to The CFO:/);
  await dialog.getByRole("radio", { name: "Keep it open" }).check();

  // Act
  await dialog.getByRole("button", { name: "Change answer" }).click();
  await expect.poll(() => posted.length).toBe(1);
  await expect(dialog.getByRole("radio", { name: "Keep it open" })).toBeChecked();
  await page.clock.runFor(1500);

  // Assert
  await expect.poll(() => posted.length).toBe(2);
  await page.clock.runFor(3000);
  expect(posted).toHaveLength(2);
  expect(posted[1].id).toBe(posted[0].id);
  await expect(dialog.locator(".answered-by")).toHaveText(/^You chose this · /);
  await expect(dialog.locator(".question-card")).not.toContainText(/did not reach|failed|Retry/);
});

test("a change the supervisor refuses puts the answer back as it stood and says why beside the button", async ({ page }) => {
  // Arrange
  await actions(page, (route) => route.fulfill({ status: 409, json: { error: "this question is no longer on the board; refresh the board" } }));
  const dialog = await reopen(page, /^Change the answer to The CFO:/);
  await dialog.getByRole("radio", { name: "Keep it open" }).check();

  // Act
  await dialog.getByRole("button", { name: "Change answer" }).click();

  // Assert
  await expect(dialog.locator(".notice")).toHaveText("This question is no longer on the board. Refresh the board.");
  await expect(dialog.locator(".answered-by")).toHaveText("You chose this · 40m ago");
});

test("a merged goblin's question says in one line that the change goes as a new instruction", async ({ page }, testInfo) => {
  // Arrange
  const posted = await actions(page);
  const dialog = await reopen(page, /^Change the answer to Marge - Tenant Wrangler:/);

  // Act
  await dialog.getByRole("radio", { name: "Wait for Prometheus" }).check();
  await dialog.getByRole("button", { name: "Change answer" }).click();

  // Assert
  await expect(dialog.locator(".change-warning")).toHaveText("Its pull request already merged. Your change goes as a new instruction.");
  await expect.poll(() => posted.length).toBe(1);
  // The CFO's mark stays inside its line, over neither its words nor the line under it.
  const mark = await dialog.locator(".answered-by .answer-mark").boundingBox();
  const words = await dialog.locator(".answered-by").boundingBox();
  const warning = await dialog.locator(".change-warning").boundingBox();
  expect(mark && words && warning && mark.y >= words.y - 1 && mark.y + mark.height <= words.y + words.height + 1 && mark.y + mark.height <= warning.y).toBe(true);
  await dialog.locator(".card-stage").screenshot({ path: testInfo.outputPath("merged.png") });
});
