import { doneCards, expect, recordDoneCards, servePages, test, type Locator, type Page, type Route } from "./site";

// The credential card in the Command Center: the Overlord pastes each value,
// and the board stores it in the project's credential scope, one row at a
// time. A value travels only in the body of its row's one save request;
// everything else names names.
const STRIPE = "cred-0123456789abcdef";
const STRIPE_GENERATION = "f".repeat(32);

// A value made at run time, so no literal in the repository looks like a key.
const canary = (prefix = "") => prefix + "canary" + crypto.randomUUID().replaceAll("-", "");

// How a Stripe test secret key starts, which the fixture's format hint for
// STRIPE_SECRET_KEY expects, so a paste of it is stored at once.
const TEST_KEY = "sk" + "_test_";

// What a value field shows for a value: one dot for each character.
const dots = (value: string) => "•".repeat(value.length);

// setByScript puts text in a field the way a script does, as an extension
// that fills forms would: it sets the value and says the field changed. No
// edit comes before it, unlike typing, pasting or Playwright's fill.
const setByScript = (field: Locator, value: string) => field.evaluate((element: HTMLInputElement, text) => {
  element.value = text;
  element.dispatchEvent(new Event("input", { bubbles: true }));
}, value);

interface Sent { path: string; token: string | null; body: unknown }

// answer records each board request the card sends and answers it with the
// next of answers, or 200 naming the names it stored.
async function answer(page: Page, answers: { status: number; json: unknown }[] = []): Promise<Sent[]> {
  const sent: Sent[] = [];
  await page.route("**/api/credentials/**", async (route: Route) => {
    const request = route.request();
    const body = request.postDataJSON();
    sent.push({ path: new URL(request.url()).pathname, token: await request.headerValue("X-CFO-Token"), body });
    await route.fulfill(answers.shift() || { status: 200, json: { id: STRIPE, state: "open", saved: Object.keys(body?.values || {}), replaced: [] } });
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

// paste puts text on the clipboard and pastes it into a field with Ctrl+V,
// as he does.
async function paste(page: Page, field: Locator, text: string) {
  await page.evaluate((value) => navigator.clipboard.writeText(value), text);
  await field.click();
  await page.keyboard.press("Control+V");
}

test.beforeEach(async ({ context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
});

test("a pasted value is stored at once, going only to the save endpoint with the board's token, and is never shown or logged again", async ({ page }) => {
  // Arrange
  const value = canary(TEST_KEY);
  const logged: string[] = [];
  page.on("console", (entry) => logged.push(entry.text()));
  const elsewhere: string[] = [];
  page.on("request", (request) => { if (!request.url().endsWith("/api/credentials/save")) elsewhere.push(request.url() + " " + (request.postData() || "")); });
  const sent = await answer(page);
  const card = await openCard(page);
  const row = card.locator('[data-credential-name="STRIPE_SECRET_KEY"]');
  let markupWhilePasted = "";
  page.on("request", (request) => { if (request.url().endsWith("/api/credentials/save")) void page.content().then((markup) => { markupWhilePasted = markup; }, () => {}); });

  // Act
  await paste(page, card.getByLabel("Value for STRIPE_SECRET_KEY"), value);

  // Assert
  await expect.poll(() => sent.length).toBe(1);
  expect(sent).toEqual([{ path: "/api/credentials/save", token: "fixture-token", body: { id: STRIPE, generation: STRIPE_GENERATION, values: { STRIPE_SECRET_KEY: value }, replace: [] } }]);
  await expect(row).toHaveAttribute("data-state", "saved");
  await expect(row).toContainText("Stored");
  await expect(row.locator("input")).toHaveCount(0);
  expect(markupWhilePasted).not.toContain(value);
  expect(await page.content()).not.toContain(value);
  expect(logged.join("\n")).not.toContain(value);
  expect(elsewhere.join("\n")).not.toContain(value);
});

test("a value field never holds the value: it shows one dot for each character, and is no password field", async ({ page }) => {
  // Arrange
  const value = canary();
  await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_SECRET_KEY");

  // Act
  await field.click();
  await page.keyboard.insertText(value);

  // Assert
  await expect(field).toHaveValue(dots(value));
  for (const [attribute, expected] of [["type", "text"], ["autocomplete", "off"], ["spellcheck", "false"]]) await expect(field).toHaveAttribute(attribute, expected);
  for (const attribute of ["name", "id"]) await expect(field).not.toHaveAttribute(attribute);
  await expect(card.getByRole("button", { name: "Store STRIPE_SECRET_KEY" })).toBeEnabled();
});

test("typing, deleting, cutting and replacing a selection edit the hidden value as they would any field, and Enter stores it", async ({ page }) => {
  // Arrange
  const sent = await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_SECRET_KEY");

  // Act
  await field.click();
  await page.keyboard.type("abcdefgh");
  await page.keyboard.press("ArrowLeft");
  await page.keyboard.press("ArrowLeft");
  await page.keyboard.press("Backspace");
  await page.keyboard.press("Home");
  await page.keyboard.press("Delete");
  await page.keyboard.type("Z");
  await page.keyboard.press("Shift+End");
  await page.keyboard.type("9xy");
  await page.keyboard.press("Shift+ArrowLeft");
  await page.keyboard.press("Control+X");
  await page.keyboard.press("Control+Z");
  const shown = await field.inputValue();
  await page.keyboard.press("Enter");

  // Assert
  expect(shown).toBe(dots("Z9x"));
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ values: { STRIPE_SECRET_KEY: "Z9x" } });
});

test("copying out of a value field copies dots, never the value", async ({ page }) => {
  // Arrange
  const value = canary();
  await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_SECRET_KEY");
  await field.click();
  await page.keyboard.insertText(value);

  // Act
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Control+C");

  // Assert
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(dots(value));
});

test("text that reaches a field itself, from a script that sets it or a composition, is taken out of it at once", async ({ page }) => {
  // Arrange
  const value = canary();
  const sent = await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_SECRET_KEY");
  const devtools = await page.context().newCDPSession(page);

  // Act
  await setByScript(field, value);
  const afterScript = await field.inputValue();
  await field.press("End");
  await devtools.send("Input.imeSetComposition", { text: "e", selectionStart: 1, selectionEnd: 1 });
  await devtools.send("Input.insertText", { text: "é" });
  const afterComposition = await field.inputValue();
  await field.press("Enter");

  // Assert
  expect(afterScript).toBe(dots(value));
  expect(afterComposition).toBe(dots(value + "é"));
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ values: { STRIPE_SECRET_KEY: value + "é" } });
});

test("a composition he abandons leaves the value as it was, and a script that sets the field later still replaces all of it", async ({ page }) => {
  // Arrange
  const first = canary();
  const second = canary();
  const sent = await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_SECRET_KEY");
  const devtools = await page.context().newCDPSession(page);
  await field.click();
  await page.keyboard.insertText(first);

  // Act
  await devtools.send("Input.imeSetComposition", { text: "e", selectionStart: 1, selectionEnd: 1 });
  await devtools.send("Input.imeSetComposition", { text: "", selectionStart: 0, selectionEnd: 0 });
  const afterAbandon = await field.inputValue();
  await setByScript(field, second);
  await field.press("Enter");

  // Assert
  expect(afterAbandon).toBe(dots(first));
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0].body).toMatchObject({ values: { STRIPE_SECRET_KEY: second } });
});

test("a script that leaves dots in a field cannot be read, so the field is emptied and nothing can be stored", async ({ page }) => {
  // Arrange
  const value = canary();
  await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_SECRET_KEY");
  await field.click();
  await page.keyboard.insertText(value);

  // Act
  await setByScript(field, dots(value) + "tail");

  // Assert
  await expect(field).toHaveValue("");
  await expect(card.getByRole("button", { name: "Store STRIPE_SECRET_KEY" })).toHaveCount(0);
});

test("dots cut out of one value field and pasted into another are refused, and the field says to copy the value again", async ({ page }) => {
  // Arrange
  const value = canary();
  const sent = await answer(page);
  const card = await openCard(page);
  const first = card.getByLabel("Value for STRIPE_SECRET_KEY");
  const second = card.getByLabel("Value for STRIPE_WEBHOOK_SECRET");
  await first.click();
  await page.keyboard.insertText(value);

  // Act
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Control+X");
  await second.click();
  await page.keyboard.press("Control+V");

  // Assert
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(dots(value));
  await expect(second).toHaveValue("");
  await expect(card.getByText("That paste was the hidden dots, not the value.", { exact: true })).toBeVisible();
  expect(sent).toEqual([]);
});

test("a name the scope already holds is replaced only once he confirms, and the pasted value waits in its field meanwhile", async ({ page }) => {
  // Arrange
  const value = canary();
  const sent = await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_WEBHOOK_SECRET");
  await expect(card.locator('[data-credential-name="STRIPE_WEBHOOK_SECRET"]')).toHaveAttribute("data-state", "held");

  // Act
  await paste(page, field, value);
  const question = page.getByRole("dialog", { name: "Replace a stored credential?" });
  await expect(question).toContainText("STRIPE_WEBHOOK_SECRET already has a value for precisiondocs");
  await question.getByRole("button", { name: "Cancel" }).click();
  const afterCancel = sent.length;
  await expect(field).toHaveValue(dots(value));
  await card.getByRole("button", { name: "Store STRIPE_WEBHOOK_SECRET" }).click();
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
  await card.getByLabel("Value for STRIPE_SECRET_KEY").press("Enter");
  await page.getByRole("dialog", { name: "Replace a stored credential?" }).getByRole("button", { name: "Replace and save" }).click();

  // Assert
  await expect.poll(() => sent.length).toBe(2);
  expect(sent.map((request) => request.body)).toEqual([
    { id: STRIPE, generation: STRIPE_GENERATION, values: { STRIPE_SECRET_KEY: value }, replace: [] },
    { id: STRIPE, generation: STRIPE_GENERATION, values: { STRIPE_SECRET_KEY: value }, replace: ["STRIPE_SECRET_KEY"] },
  ]);
});

// The live STRIPE_TEST_SECRET_KEY once arrived as a single character and
// still showed stored: the board now refuses a value of the wrong shape.
test("a value the board refuses says why on its row, which stays open with its field empty and no check", async ({ page }) => {
  // Arrange
  const refusal = "STRIPE_SECRET_KEY does not start with sk_ or rk_, so it is not a Stripe secret key. Nothing was stored.";
  const sent = await answer(page, [{ status: 400, json: { error: refusal } }]);
  const card = await openCard(page);
  const row = card.locator('[data-credential-name="STRIPE_SECRET_KEY"]');

  // Act
  await paste(page, card.getByLabel("Value for STRIPE_SECRET_KEY"), canary(TEST_KEY));

  // Assert
  await expect.poll(() => sent.length).toBe(1);
  await expect(row.getByRole("alert")).toHaveText(refusal);
  await expect(row).toHaveAttribute("data-state", "needed");
  await expect(card.getByLabel("Value for STRIPE_SECRET_KEY")).toHaveValue("");
  await expect(row.getByRole("img", { name: "Not stored yet" })).toBeVisible();
});

test("a pasted value its format hint warns about waits for his Enter instead of storing at once", async ({ page }) => {
  // Arrange
  const sent = await answer(page);
  const card = await openCard(page);
  const field = card.getByLabel("Value for STRIPE_SECRET_KEY");

  // Act
  await paste(page, field, canary("sk" + "_live_"));
  await expect(card.getByRole("img", { name: /use a restricted rk_live_ key/ })).toHaveAttribute("data-tip", /use a restricted rk_live_ key/);
  const beforeEnter = sent.length;
  await field.press("Enter");

  // Assert
  expect(beforeEnter).toBe(0);
  await expect.poll(() => sent.length).toBe(1);
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

test("while the request's terminal is open the card says so and takes no value or second terminal", async ({ page }) => {
  // Arrange
  await answer(page);
  const card = await openCard(page);

  // Act
  await page.evaluate((id) => window.board?.runs([{ id: "credential-1", identity: "c".repeat(64), title: "Type STRIPE_SECRET_KEY", shell: "powershell", state: "running", created_at: "2026-10-01T03:10:00Z", credential_request: id, credential_names: ["STRIPE_SECRET_KEY"] }]), STRIPE);

  // Assert
  await expect(card.getByRole("status")).toContainText("A terminal is open on this PC");
  await expect(card.getByLabel("Value for STRIPE_SECRET_KEY")).toBeDisabled();
  await expect(card.getByRole("button", { name: "Run in a terminal on this PC" })).toBeDisabled();
});

// The terminal runs on the card itself, where he types each value, on the
// board on this PC.
test("the request's terminal runs on its card, where he types each value", async ({ page }) => {
  // Arrange
  const views: string[] = [];
  await page.routeWebSocket("**/api/terminal/native?*", (socket) => {
    views.push(socket.url());
    socket.send(JSON.stringify({ type: "history", bytes: 0 }));
  });
  await answer(page);
  const card = await openCard(page);

  // Act
  await page.evaluate((id) => window.board?.runs([{ id: "credential-1", identity: "c".repeat(64), title: "Type STRIPE_SECRET_KEY", shell: "powershell", state: "running", terminal: true, created_at: "2026-10-01T03:10:00Z", credential_request: id, credential_names: ["STRIPE_SECRET_KEY"] }]), STRIPE);

  // Assert
  await expect(card.locator(".credential-terminal-open")).toContainText("Type each value in the terminal below");
  await expect(card.getByRole("region", { name: "Terminal of Type STRIPE_SECRET_KEY" })).toBeVisible();
  await expect.poll(() => views.map((url) => new URL(url).searchParams.get("run"))).toContain("credential-1");
  await expect(card.getByLabel("Value for STRIPE_SECRET_KEY")).toBeDisabled();
});

// Whether he pasted each value in the card or typed them in its terminal,
// the request closes once every value is stored, and the card finishes as
// every other command does: it says so and the next item follows by itself.
for (const route of ["card", "terminal"] as const) {
  test("once every value is stored in the " + route + " the Command Center says Stored and moves on by itself, never leaving a card whose only way on is Skip", async ({ page }) => {
    // Arrange
    await answer(page);
    const card = await openCard(page);
    await recordDoneCards(page);

    // Act
    if (route === "card") await paste(page, card.getByLabel("Value for STRIPE_SECRET_KEY"), canary(TEST_KEY));
    else await page.evaluate((id) => window.board?.runs([{ id: "credential-1", identity: "c".repeat(64), title: "Type STRIPE_SECRET_KEY", shell: "powershell", state: "running", created_at: "2026-10-01T03:10:00Z", credential_request: id, credential_names: ["STRIPE_SECRET_KEY"] }]), STRIPE);
    await page.evaluate(([id, typed]) => {
      window.board?.runs([]);
      window.board?.request(id, { state: "saved", saved: ["STRIPE_SECRET_KEY"], typed: typed ? ["STRIPE_SECRET_KEY"] : [], closed_at: "2026-10-01T03:20:00Z" });
    }, [STRIPE, route === "terminal"] as const);

    // Assert
    await expect.poll(() => doneCards(page)).toContainEqual(expect.stringMatching(/^Stored.*Saved STRIPE_SECRET_KEY for precisiondocs/));
    await expect(page.getByRole("dialog").locator('[data-credential-request="cred-fedcba9876543210"]')).toBeVisible();
    await expect(card).toHaveCount(0);
  });
}

test("a board opened from another machine shows no value field and no Run, only the commands to copy", async ({ page }) => {
  // Arrange: the board takes values only under 127.0.0.1, localhost or ::1,
  // and this origin is none of them.
  const elsewhere = "https://board.test";
  await servePages(page.context(), elsewhere);
  await answer(page);

  // Act
  const card = await openCard(page, elsewhere);

  // Assert
  await expect(card).toContainText("Values can only be typed on the board on your PC");
  await expect(card.locator("input")).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Run in a terminal on this PC" })).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Copy the commands" })).toBeVisible();
  await expect(card.locator('[data-credential-name="STRIPE_WEBHOOK_SECRET"]')).toContainText("Stored before: saving replaces it");
});

test("the header gives the steps to get the values numbered in order, the page to open first, each starting with a capital", async ({ page }) => {
  // Arrange
  await answer(page);

  // Act
  const card = await openCard(page);
  const list = card.getByRole("list", { name: "How to get them" });
  const steps = list.getByRole("listitem");

  // Assert
  await expect(steps).toHaveText(["Open dashboard.stripe.com/test/apikeys", "Sign in to the shop's Stripe account", "Reveal the test secret key and copy it"]);
  expect(await list.evaluate((element) => [element.tagName, getComputedStyle(element).listStyleType])).toEqual(["OL", "decimal"]);
  await expect(steps.first().getByRole("link")).toHaveAttribute("href", "https://dashboard.stripe.com/test/apikeys");
});

test("each row's mark says in its tip where its value goes, and how it was stored once it is", async ({ page }) => {
  // Arrange
  await answer(page);
  const card = await openCard(page);
  const row = card.locator('[data-credential-name="STRIPE_SECRET_KEY"]');
  await expect(row.getByRole("img", { name: "Not stored yet" })).toHaveAttribute("data-tip", "Goes to the precisiondocs vault for its goblins and the stripe service");
  await expect(card.locator('[data-credential-name="STRIPE_WEBHOOK_SECRET"]').getByRole("img", { name: "Stored before" })).toHaveAttribute("data-tip", "Already in the precisiondocs vault");

  // Act
  await page.evaluate((id) => window.board?.request(id, { saved: ["STRIPE_SECRET_KEY"], typed: ["STRIPE_SECRET_KEY"] }), STRIPE);

  // Assert
  await expect(row).toHaveAttribute("data-state", "saved");
  await expect(row.locator(".credential-stored")).toHaveAttribute("data-tip", "Stored in the precisiondocs vault, typed in the terminal");
  await expect(row.locator("input")).toHaveCount(0);
});

test("a request whose values also go to an env file says so in its row's tip", async ({ page }) => {
  // Arrange
  await answer(page);
  await openCard(page);

  // Act
  await page.getByRole("dialog").getByRole("button", { name: "Back", exact: true }).click();
  const row = page.getByRole("dialog").locator('[data-credential-request="cred-fedcba9876543210"] [data-credential-name="DATABASE_URL"]');

  // Assert
  await expect(row.getByRole("img", { name: "Not stored yet" })).toHaveAttribute("data-tip", "Goes to the precisiondocs vault for its goblins, and into .env.docker.local");
});

test("an expired request says it is closed, with why in its tip, and takes nothing", async ({ page }) => {
  // Arrange
  await answer(page);
  const card = await openCard(page);

  // Act
  await page.evaluate((id) => window.board?.request(id, { state: "expired", reason: "Nobody saved it within 24 hours.", closed_at: "2026-10-02T03:00:00Z" }), STRIPE);

  // Assert
  await expect(card.locator(".credential-outcome")).toHaveText("Closed");
  await expect(card.locator(".credential-outcome")).toHaveAttribute("data-tip", "Nobody saved it within 24 hours.");
  await expect(card.locator("input")).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Run in a terminal on this PC" })).toHaveCount(0);
});

// He had to scroll a five column table that was clipped at his window: the
// card is now a Name and a Value column and nothing else.
for (const [size, viewport, scale] of [["his window", { width: 1707, height: 1067 }, 1.5], ["a phone's width", { width: 390, height: 844 }, 3]] as const) {
  test.describe(size, () => {
    test.use({ viewport, deviceScaleFactor: scale });

    test("at " + size + " the table is a Name and a Value column that fits the card with nothing cut off and no sideways scrolling", async ({ page }) => {
      // Arrange
      await answer(page);

      // Act
      const card = await openCard(page);
      const fit = await card.evaluate((element) => {
        const box = element.getBoundingClientRect();
        const table = element.querySelector("table")!;
        const inside = (part: Element) => { const own = part.getBoundingClientRect(); return own.left >= box.left - 0.5 && own.right <= box.right + 0.5; };
        const dialog = element.closest("dialog")!;
        return {
          headers: [...table.querySelectorAll("thead th")].map((header) => header.textContent),
          cells: [...table.querySelectorAll("tbody tr")].map((row) => row.children.length),
          scrolls: dialog.scrollWidth > dialog.clientWidth || element.scrollWidth > element.clientWidth,
          clipped: [...table.querySelectorAll("tbody th, tbody td, input")].filter((part) => !inside(part) || part.scrollWidth > part.clientWidth + 1).length,
        };
      });

      // Assert
      expect(fit.headers).toEqual(["Name", "Value"]);
      expect(fit.cells).toEqual([2, 2]);
      expect(fit.scrolls).toBe(false);
      expect(fit.clipped).toBe(0);
      await expect(card).not.toContainText(/Saved to|Used for|Get it here|Each value is saved only/i);
    });
  });
}

// The theme of 1 October 2026 dropped tokens the card still read, and its
// borders and backgrounds went with them without a test saying so.
test("every style token the card reads is one the board's theme defines", async ({ page }) => {
  // Arrange
  await answer(page);
  const card = await openCard(page);

  // Act
  const tokens = await card.evaluate((element) => {
    const defined = getComputedStyle(element);
    const sheet = [...document.styleSheets].find((candidate) => [...candidate.cssRules].some((rule) => rule.cssText.includes(".credential-table")));
    const read = new Set<string>();
    for (const rule of sheet?.cssRules || []) for (const found of rule.cssText.matchAll(/var\((--[\w-]+)/g)) read.add(found[1]);
    return { read: read.size, missing: [...read].filter((token) => !defined.getPropertyValue(token).trim()) };
  });

  // Assert
  expect(tokens.read).toBeGreaterThan(5);
  expect(tokens.missing).toEqual([]);
});
