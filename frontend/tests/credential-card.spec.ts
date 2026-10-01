import { expect, test, type Page, type Route } from "@playwright/test";

// The credential card in the Command Center: the Overlord pastes each value,
// and the board stores it in the project's credential scope. A value travels
// only in the body of the one save request; everything else names names.
const STRIPE = "cred-0123456789abcdef";
const STRIPE_GENERATION = "f".repeat(32);

// A value made at run time, so no literal in the repository looks like a key.
const canary = (prefix = "") => prefix + "canary" + crypto.randomUUID().replaceAll("-", "");

interface Sent { path: string; token: string | null; body: unknown }

// answer records each board request the card sends and answers it with the
// next of answers, or 200 with no names.
async function answer(page: Page, answers: { status: number; json: unknown }[] = []): Promise<Sent[]> {
  const sent: Sent[] = [];
  await page.route("**/api/credentials/**", async (route: Route) => {
    const request = route.request();
    sent.push({ path: new URL(request.url()).pathname, token: await request.headerValue("X-CFO-Token"), body: request.postDataJSON() });
    await route.fulfill(answers.shift() || { status: 200, json: {} });
  });
  return sent;
}

async function openCard(page: Page, origin = "") {
  await page.goto(origin + "/tests/fixtures/credential-card.html");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  const card = page.getByRole("dialog").locator(`[data-credential-request="${STRIPE}"]`);
  await expect(card).toBeVisible();
  return card;
}

test("a pasted value goes only to the save endpoint with the board's token, and its field is emptied", async ({ page, context }) => {
  // Arrange
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const value = canary();
  const logged: string[] = [];
  page.on("console", (entry) => logged.push(entry.text()));
  const sent = await answer(page, [{ status: 200, json: { id: STRIPE, state: "saved", saved: ["STRIPE_SECRET_KEY"], replaced: [] } }]);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_SECRET_KEY");

  // Act
  await page.evaluate((text) => navigator.clipboard.writeText(text), value);
  await field.click();
  await page.keyboard.press("Control+V");
  await expect(field).toHaveValue(value);
  const markupWhileTyped = await page.content();
  await card.getByRole("button", { name: "Save" }).click();

  // Assert
  expect(markupWhileTyped).not.toContain(value);
  await expect.poll(() => sent.length).toBe(1);
  expect(sent).toEqual([{ path: "/api/credentials/save", token: "fixture-token", body: { id: STRIPE, generation: STRIPE_GENERATION, values: { STRIPE_SECRET_KEY: value }, replace: [] } }]);
  await expect(field).toHaveValue("");
  for (const attribute of [["type", "password"], ["autocomplete", "new-password"], ["spellcheck", "false"]]) await expect(field).toHaveAttribute(attribute[0], attribute[1]);
  expect(await page.content()).not.toContain(value);
  expect(logged.join("\n")).not.toContain(value);
});

test("a name the scope already holds is replaced only once he confirms, and the pasted value waits in its field meanwhile", async ({ page }) => {
  // Arrange
  const value = canary();
  const sent = await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_WEBHOOK_SECRET");
  await expect(card.locator('[data-credential-name="STRIPE_WEBHOOK_SECRET"]')).toHaveAttribute("data-state", "held");

  // Act
  await field.fill(value);
  await card.getByRole("button", { name: "Save" }).click();
  const question = page.getByRole("dialog", { name: "Replace a stored credential?" });
  await expect(question).toContainText("STRIPE_WEBHOOK_SECRET already has a value for precisiondocs");
  await question.getByRole("button", { name: "Cancel" }).click();
  const afterCancel = sent.length;
  await expect(field).toHaveValue(value);
  await card.getByRole("button", { name: "Save" }).click();
  await page.getByRole("dialog", { name: "Replace a stored credential?" }).getByRole("button", { name: "Replace and save" }).click();

  // Assert
  expect(afterCancel).toBe(0);
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toEqual({ id: STRIPE, generation: STRIPE_GENERATION, values: { STRIPE_WEBHOOK_SECRET: value }, replace: ["STRIPE_WEBHOOK_SECRET"] });
});

test("when the board finds a name stored since the card last looked, the card asks before replacing it", async ({ page }) => {
  // Arrange
  const value = canary();
  const sent = await answer(page, [{ status: 409, json: { error: "STRIPE_SECRET_KEY already holds a value for precisiondocs; confirm replacing it.", existing: ["STRIPE_SECRET_KEY"] } }]);
  const card = await openCard(page);

  // Act
  await card.getByLabel("Value for STRIPE_SECRET_KEY").fill(value);
  await card.getByRole("button", { name: "Save" }).click();
  await page.getByRole("dialog", { name: "Replace a stored credential?" }).getByRole("button", { name: "Replace and save" }).click();

  // Assert
  await expect.poll(() => sent.length).toBe(2);
  expect(sent.map((request) => request.body)).toEqual([
    { id: STRIPE, generation: STRIPE_GENERATION, values: { STRIPE_SECRET_KEY: value }, replace: [] },
    { id: STRIPE, generation: STRIPE_GENERATION, values: { STRIPE_SECRET_KEY: value }, replace: ["STRIPE_SECRET_KEY"] },
  ]);
});

// CFO decision 3624: Run and the card behave the same.
test("Run opens a terminal on this PC for the names the scope does not hold, and asks before one that types a stored name", async ({ page }) => {
  // Arrange
  const sent = await answer(page);
  const card = await openCard(page);

  // Act
  const lines = await card.locator(".credential-terminal code").allTextContents();
  await card.getByRole("button", { name: "Run in a terminal on this PC" }).click();
  await expect.poll(() => sent.length).toBe(1);
  await page.evaluate((id) => window.board?.request(id, { saved: ["STRIPE_SECRET_KEY"], typed: ["STRIPE_SECRET_KEY"] }), STRIPE);
  await card.getByRole("button", { name: "Run in a terminal on this PC" }).click();
  await page.getByRole("dialog", { name: "Replace a stored credential?" }).getByRole("button", { name: "Replace and run" }).click();

  // Assert
  expect(lines).toEqual(["cfo auth store --project precisiondocs STRIPE_SECRET_KEY"]);
  await expect.poll(() => sent.length).toBe(2);
  expect(sent.map((request) => [request.path, request.body])).toEqual([
    ["/api/credentials/terminal", { id: STRIPE, generation: STRIPE_GENERATION, replace: [] }],
    ["/api/credentials/terminal", { id: STRIPE, generation: STRIPE_GENERATION, replace: ["STRIPE_WEBHOOK_SECRET"] }],
  ]);
});

test("while the request's terminal is open the card says so and takes no save or second terminal", async ({ page }) => {
  // Arrange
  await answer(page);
  const card = await openCard(page);

  // Act
  await page.evaluate((id) => window.board?.runs([{ id: "credential-1", identity: "c".repeat(64), title: "Type STRIPE_SECRET_KEY", shell: "powershell", state: "running", created_at: "2026-10-01T03:10:00Z", credential_request: id, credential_names: ["STRIPE_SECRET_KEY"] }]), STRIPE);
  await card.getByLabel("Value for STRIPE_SECRET_KEY").fill(canary());

  // Assert
  await expect(card.getByRole("status")).toContainText("A terminal is open on this PC");
  await expect(card.getByRole("button", { name: "Save" })).toBeDisabled();
  await expect(card.getByRole("button", { name: "Run in a terminal on this PC" })).toBeDisabled();
});

test("a board opened from another machine shows no value field and no Run, only the commands to copy", async ({ page }) => {
  // Arrange: Chromium sends board.localhost to this machine, and the board
  // takes values only under 127.0.0.1, localhost or ::1.
  await answer(page);

  // Act
  const card = await openCard(page, "http://board.localhost:5188");

  // Assert
  await expect(card).toContainText("Values can only be typed on the board on your PC");
  await expect(card.locator("input")).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Save" })).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Run in a terminal on this PC" })).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Copy the commands" })).toBeVisible();
  await expect(card.locator('[data-credential-name="STRIPE_WEBHOOK_SECRET"]')).toContainText("Stored before: saving replaces it");
});

test("each row says where its value goes, and a saved request checks each row and says who was told", async ({ page }) => {
  // Arrange
  await answer(page);
  const card = await openCard(page);
  const row = card.locator('[data-credential-name="STRIPE_SECRET_KEY"]');
  await expect(row).toContainText("Repository precisiondocs");
  await expect(row).toContainText("Credential scope precisiondocs");
  await expect(row).toContainText("Goblins' auth.ps1 · stripe service");

  // Act
  await page.evaluate((id) => window.board?.request(id, { state: "saved", saved: ["STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET"], replaced: ["STRIPE_WEBHOOK_SECRET"], typed: ["STRIPE_SECRET_KEY"], told: ["add-billing"], closed_at: "2026-10-01T03:20:00Z" }), STRIPE);

  // Assert
  await expect(card).toContainText("Saved STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET for precisiondocs");
  await expect(card).toContainText("1 running goblin was told to reload its credentials.");
  await expect(row).toHaveAttribute("data-state", "saved");
  await expect(row).toContainText("Typed in the terminal");
  await expect(card.locator('[data-credential-name="STRIPE_WEBHOOK_SECRET"]')).toContainText("Replaced");
  await expect(row).toContainText("Credential scope precisiondocs");
  await expect(card.locator("input")).toHaveCount(0);
});

test("a value that starts the way its format hint warns about is flagged and can still be saved", async ({ page }) => {
  // Arrange
  const sent = await answer(page);
  const card = await openCard(page);

  // Act
  await card.getByLabel("Value for STRIPE_SECRET_KEY").fill(canary("sk" + "_live_"));

  // Assert
  await expect(card).toContainText("use a restricted rk_live_ key");
  await card.getByRole("button", { name: "Save" }).click();
  await expect.poll(() => sent.length).toBe(1);
});

test("an expired request says why and takes nothing", async ({ page }) => {
  // Arrange
  await answer(page);
  const card = await openCard(page);

  // Act
  await page.evaluate((id) => window.board?.request(id, { state: "expired", reason: "Nobody saved it within 24 hours.", closed_at: "2026-10-02T03:00:00Z" }), STRIPE);

  // Assert
  await expect(card).toContainText("Nobody saved it within 24 hours.");
  await expect(card.locator("input")).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Save" })).toHaveCount(0);
});
