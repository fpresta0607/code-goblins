import { expect, test, type Page } from "./site";

test.use({ locale: "en-US", timezoneId: "America/Chicago", deviceScaleFactor: 1.5 });

const NOW = new Date("2026-10-15T13:45:00Z");

async function history(page: Page) {
  await page.clock.install({ time: NOW });
  await page.goto("/tests/fixtures/history-same.html");
  await page.locator(".command-center-menu > summary").click();
  const panel = page.locator(".command-center-updates");
  await panel.getByText("History").click();
  return { panel, rows: panel.locator(".inbox-history .inbox-list > li") };
}

// reopens records each card the board asks the supervisor to bring back, and
// answers it with status and body.
async function reopens(page: Page, status = 200, body: Record<string, unknown> = {}) {
  const posted: Record<string, unknown>[] = [];
  await page.route("**/api/credentials/reopen", async (route) => {
    const sent = route.request().postDataJSON() as Record<string, unknown>;
    posted.push(sent);
    await route.fulfill({ status, json: status === 200 ? { id: sent.id } : body });
  });
  return posted;
}

const MARKS: [string, RegExp][] = [
  ["May I file the push key card now?", /^The CFO answered$/],
  ["Tick the App Group for the share extension", /^Done$/],
  ["Move the sign-in screen out of test mode", /^Closed\./],
  ["Turn on time sensitive notifications", /^You answered: The box was already ticked$/],
  ["Paste APNS_PRIVATE_KEY for sample-app", /^Saved APNS_PRIVATE_KEY/],
  ["Enter deploy channel for sample-app", /^Sent DEPLOY_CHANNEL/],
  ["Paste APNS_KEY_ID for sample-app", /^Saved APNS_KEY_ID/],
  ["Accept the new developer terms", /^Closed\./],
  ["Merge the release pull request?", /^You answered$/],
  ["Add the DNS records for the docs site", /^You wrote: Added both records$/],
  ["Publish release v0.5.2", /exit 0/],
  ["Which accent should the board use?", /^The CFO answered while you were away$/],
  ["Should the sign-in page keep its old link?", /^Superseded/],
  ["Sign in to Example for sample-app", /^Signed in to Example/],
];

for (const [width, height] of [[1707, 1067], [390, 844]]) {
  test(`every History row leads with the CFO's goblin, marked with how it ended, and has the pencil, at ${width}px`, async ({ page }, testInfo) => {
    // Arrange
    await page.setViewportSize({ width, height });

    // Act
    const { panel, rows } = await history(page);
    await expect(rows).toHaveCount(MARKS.length);
    await panel.screenshot({ path: testInfo.outputPath(`history-same-${width}.png`), animations: "disabled" });

    // Assert
    for (const [text, label] of MARKS) {
      const row = rows.filter({ hasText: text });
      const mark = row.locator(".history-mark");
      await expect(mark, text).toHaveCount(1);
      await expect(mark.locator(".goblin-avatar.persona-cfo"), text).toBeVisible();
      await expect(mark.locator(".mark-badge"), text).toBeVisible();
      await expect(mark, text).toHaveAttribute("role", "img");
      await expect(mark, text).toHaveAttribute("aria-label", label);
      await expect(row.locator(".history-change"), text).toHaveCount(1);
      await expect(row.locator(".history-change .icon"), text).toBeVisible();
    }
    const leading = await rows.evaluateAll((items) => items.map((item) => item.firstElementChild?.classList.contains("history-mark")));
    expect(leading, "every row leads with the mark").toEqual(MARKS.map(() => true));
    for (const head of await rows.locator(".history-head").all()) {
      expect(await head.evaluate((element) => Math.round(element.getBoundingClientRect().height / parseFloat(getComputedStyle(element).lineHeight))), "each row stays one line").toBe(1);
    }
    expect(await panel.evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);
  });
}

test("the pencil on a question changes its answer", async ({ page }) => {
  // Arrange
  const posted = await reopens(page);
  const { rows } = await history(page);

  // Act
  await rows.filter({ hasText: "May I file the push key card now?" }).locator(".history-change").click();

  // Assert
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("radio", { name: "Yes" })).toBeChecked();
  await expect(dialog.locator(".answered-by")).toContainText("The CFO chose this");
  expect(posted).toHaveLength(0);
});

for (const [text, id, generation] of [
  ["Tick the App Group for the share extension", "cred-1111111111111111", "1".repeat(32)],
  ["Move the sign-in screen out of test mode", "cred-2222222222222222", "2".repeat(32)],
  ["Turn on time sensitive notifications", "cred-3333333333333333", "3".repeat(32)],
] as const) {
  test(`the pencil on a step card brings it back to redo: ${text}`, async ({ page }) => {
    // Arrange
    const posted = await reopens(page);
    const { rows } = await history(page);
    const pencil = rows.filter({ hasText: text }).locator(".history-change");
    await expect(pencil).toHaveAttribute("data-tip", "Bring back");

    // Act
    await pencil.click();
    await expect.poll(() => posted.length).toBe(1);
    await page.evaluate((card) => window.cardBack?.(card), id);

    // Assert
    expect(posted[0]).toEqual({ id, generation });
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(text);
    await expect(dialog.getByRole("button", { name: /Done|Dismiss/ }).first()).toBeEnabled();
  });
}

test("a card the supervisor will not bring back says why on its row", async ({ page }) => {
  // Arrange
  await reopens(page, 409, { error: "This card is withdrawn, so it does not come back." });
  const { rows } = await history(page);
  const row = rows.filter({ hasText: "Tick the App Group for the share extension" });

  // Act
  await row.locator(".history-change").click();

  // Assert
  await expect(row.locator(".click-feedback")).toHaveText("This card is withdrawn, so it does not come back.");
  await expect(page.getByRole("dialog")).toBeHidden();
});

test("bringing a card back sends once and waits for its new generation", async ({ page }) => {
  // Arrange
  const posted = await reopens(page);
  const { rows } = await history(page);
  const pencil = rows.filter({ hasText: "Tick the App Group for the share extension" }).locator(".history-change");

  // Act
  await pencil.click();
  await expect.poll(() => posted.length).toBe(1);
  await pencil.dispatchEvent("click");

  // Assert
  await expect(pencil).toHaveAttribute("aria-disabled", "true");
  await expect(page.getByRole("dialog")).toBeHidden();
  expect(posted).toHaveLength(1);
  await page.evaluate(() => window.cardBack?.("cred-1111111111111111"));
  await expect(page.getByRole("dialog")).toContainText("Tick the App Group for the share extension");
});

for (const [text, why] of [
  ["Paste APNS_PRIVATE_KEY for sample-app", "Its values are stored"],
  ["Enter deploy channel for sample-app", "Its values are stored"],
  ["Paste APNS_KEY_ID for sample-app", "Its values are stored"],
  ["Accept the new developer terms", "It expired"],
  ["Add the DNS records for the docs site", "A page or a wait stays as it closed"],
  ["Publish release v0.5.2", "A command that ran stays as it ran"],
  ["Should the sign-in page keep its old link?", "It has no answer to change"],
  ["Sign in to Example for sample-app", "This sign-in has finished"],
] as const) {
  test(`the pencil on a row nothing can change says why and does nothing: ${text}`, async ({ page }) => {
    // Arrange
    const posted = await reopens(page);
    const { rows } = await history(page);
    const pencil = rows.filter({ hasText: text }).locator(".history-change");

    // Act
    await pencil.dispatchEvent("click");

    // Assert
    await expect(pencil).toHaveAttribute("aria-disabled", "true");
    await expect(pencil).toHaveAttribute("data-tip", why);
    await expect(pencil).toHaveAccessibleName(new RegExp("^Nothing to change: .*" + text.replace(/[?.]/g, "\\$&")));
    await expect(page.getByRole("dialog")).toBeHidden();
    expect(posted).toHaveLength(0);
  });
}

for (const { text, steps, values } of [
  { text: "Paste APNS_PRIVATE_KEY for sample-app", steps: ["Open developer.example.com/account/keys", "Download the .p8 key"], values: [["APNS_PRIVATE_KEY", "Already stored"]] },
  { text: "Enter deploy channel for sample-app", steps: ["Pick the channel the team reads"], values: [["Deploy channel", "Already sent to Hank - Key Keeper"]] },
] as const) {
  test(`the caret on a card whose values are stored opens its steps, each value already stored, with nothing to change it: ${text}`, async ({ page }) => {
    // Arrange
    const posted = await reopens(page);
    const { rows } = await history(page);
    const row = rows.filter({ hasText: text });
    const detail = row.locator(".history-detail");
    await expect(detail).toBeHidden();

    // Act
    await row.locator(".history-open").click();

    // Assert
    await expect(detail).toBeVisible();
    await expect(detail.getByRole("list", { name: "How to get them" }).getByRole("listitem")).toHaveText([...steps]);
    const shown = detail.getByRole("list", { name: "Its values" });
    for (const [name, said] of values) await expect(shown, name).toContainText(name + said);
    await expect(detail.locator("input, textarea, [contenteditable]")).toHaveCount(0);
    await expect(detail.getByRole("button")).toHaveCount(1);
    await expect(detail.getByRole("button")).toHaveAccessibleName("Something else?");
    await expect(row.locator(".history-change")).toHaveAttribute("aria-disabled", "true");
    expect(await row.evaluate((element) => element.scrollWidth - element.clientWidth)).toBe(0);
    await page.locator(".command-center-updates").screenshot({ path: test.info().outputPath("history-stored-open.png"), animations: "disabled" });
    expect(posted).toHaveLength(0);
  });
}

test("Something else? on a card whose values are stored sends his words to its asker", async ({ page }) => {
  // Arrange
  const sent: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    sent.push(body);
    await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  });
  const { rows } = await history(page);
  const row = rows.filter({ hasText: "Paste APNS_PRIVATE_KEY for sample-app" });
  await row.locator(".history-open").click();

  // Act
  await row.getByRole("button", { name: "Something else?" }).click();
  const note = page.getByRole("dialog", { name: "Something else?" });
  await note.getByRole("textbox").fill("Use the key from the team account instead");
  await note.getByRole("button", { name: "Send" }).click();

  // Assert
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0]).toMatchObject({ kind: "message", task_id: "push-settings", generation: "", text: "About your card cred-4444444444444444 (The push key for the gateways), whose values are stored: Use the key from the team account instead" });
  await expect(note).toBeHidden();
  await expect(row.locator(".history-stored .click-feedback")).toHaveText("Sent to Hank - Key Keeper.");
  await expect(row.locator(".history-detail")).toBeVisible();
});

test("Something else? on a stored card whose goblin has ended sends his words to the CFO, naming its task", async ({ page }) => {
  // Arrange
  const sent: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    sent.push(body);
    await route.fulfill({ status: 202, json: { ...body, status: "queued" } });
  });
  const { rows } = await history(page);
  const row = rows.filter({ hasText: "Paste APNS_KEY_ID for sample-app" });
  await row.locator(".history-open").click();
  const button = row.getByRole("button", { name: "Something else?" });
  await expect(button).toHaveAttribute("data-tip", "Write to the CFO about this card");

  // Act
  await button.click();
  const note = page.getByRole("dialog", { name: "Something else?" });
  await expect(note).toContainText("It goes to the CFO as your word.");
  await note.getByRole("textbox").fill("File the key ID again under the team account");
  await note.getByRole("button", { name: "Send" }).click();

  // Assert
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0]).toMatchObject({ kind: "message", task_id: "", generation: "", text: "About the card cred-7777777777777777 of task cg-push-retired (The key ID for the gateways), whose values are stored: File the key ID again under the team account" });
  await expect(row.locator(".history-stored .click-feedback")).toHaveText("Sent to the CFO.");
});

test("Something else? resent after a lost answer keeps its ID only for the same words", async ({ page }) => {
  // Arrange
  const sent: Record<string, unknown>[] = [];
  await page.route("**/api/actions", async (route) => {
    sent.push(route.request().postDataJSON() as Record<string, unknown>);
    await route.abort("connectionreset");
  });
  const { rows } = await history(page);
  const row = rows.filter({ hasText: "Paste APNS_PRIVATE_KEY for sample-app" });
  await row.locator(".history-open").click();
  const write = async (words: string) => {
    await row.getByRole("button", { name: "Something else?" }).click();
    const note = page.getByRole("dialog", { name: "Something else?" });
    await note.getByRole("textbox").fill(words);
    await note.getByRole("button", { name: "Send" }).click();
    await expect(row.getByRole("button", { name: "Something else?" })).toBeEnabled();
  };

  // Act
  await write("Use the key from the team account instead");
  await write("Use the key from the team account instead");
  await write("Use the old key after all");

  // Assert
  await expect.poll(() => sent.length).toBe(3);
  expect(sent[1].id, "the same words resend under the same ID").toBe(sent[0].id);
  expect(sent[2].id, "other words take a fresh ID").not.toBe(sent[0].id);
  expect(sent[2].text).toBe("About your card cred-4444444444444444 (The push key for the gateways), whose values are stored: Use the old key after all");
});
