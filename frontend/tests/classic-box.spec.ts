import { expect, test, type Page, type Route } from "./site";
import { box, field, NAME, open, openGoblin, pickStyle, type Relay } from "./session-board";

// The Overlord, 2026-11-10, of the CEO's terminal in Classic with the board's
// message box under it, empty but for its placeholder, while a turn ran: "why
// is terminal showing the chat box that's not supposed to be there" (#2327).
// Classic draws the terminal alone, and a box stays under it only while it has
// work of its own: a recording, an upload, a Send still landing, or a failed
// Send to retry. The box counted every refusal it had shown as a failed Send
// until its words changed, so a key it could not press or a file it could not
// keep held it there long after the refusal's banner had faded.

const terminalInput = (page: Page) => page.getByRole("textbox", { name: "Terminal input", exact: true });
const refusal = (page: Page, name: string) => box(page, name).getByRole("alert");

// openInTurn opens who's panel in Modern chat with a turn under way.
async function openInTurn(page: Page, who: "CEO" | "goblin") {
  const relay = await open(page);
  const name = who === "CEO" ? "CEO" : NAME;
  if (who === "CEO") await page.keyboard.press("Control+Alt+1");
  else await openGoblin(page);
  await expect(page.locator("#panel-title")).toHaveText(name);
  await expect.poll(() => relay.views).toBeGreaterThan(0);
  relay.say({ type: "screen", asking: false, open: false, working: true });
  await expect(box(page, name).getByRole("button", { name: "Stop", exact: true })).toBeVisible();
  return { relay, name };
}

// pausedKey has the box refuse its mode key, Shift+Tab, while typing is
// paused, over draft.
const pausedKey = (draft: string) => async (page: Page, name: string, relay: Relay) => {
  relay.say({ type: "held", reason: "pipeline owns this task" });
  await expect(box(page, name).getByRole("button", { name: "Stop", exact: true })).toHaveCount(0);
  await field(page, name).fill(draft);
  await field(page, name).press("Shift+Tab");
};

// lostFile has the box refuse a file the board could not keep.
const lostFile = async (page: Page, name: string) => {
  await page.route("**/api/composer/file**", (route: Route) => route.fulfill({ status: 413, json: { error: "A pasted file may be at most 10 MiB." } }));
  await box(page, name).locator('input[type="file"]').setInputFiles({ name: "notes.md", mimeType: "text/markdown", buffer: Buffer.from("# notes") });
};

// Each is a refusal that was no Send, so nothing waits in the box for it.
const REFUSALS: { of: string; says: string; cause: (page: Page, name: string, relay: Relay) => Promise<void> }[] = [
  { of: "a key while typing was paused", says: "Typing is paused.", cause: pausedKey("") },
  { of: "a key while typing was paused, over a draft it holds", says: "Typing is paused.", cause: pausedKey("not sent yet") },
  { of: "a file it could not keep", says: "That file was not added: A pasted file may be at most 10 MiB.", cause: lostFile },
];

for (const who of ["CEO", "goblin"] as const) {
  for (const refused of REFUSALS) {
    test(`Classic draws the ${who}'s terminal alone during a turn after its box refused ${refused.of}`, async ({ page }) => {
      // Arrange
      const { relay, name } = await openInTurn(page, who);
      await refused.cause(page, name, relay);
      await expect(refusal(page, name)).toHaveText(refused.says);

      // Act
      await pickStyle(page, "Terminal");

      // Assert
      await expect(terminalInput(page)).toBeVisible();
      await expect(box(page, name)).toBeHidden();
      await expect(page.locator(".classic-queue")).toBeHidden();
    });
  }

  test(`a Send from the ${who}'s box keeps it under the Classic terminal while it lands, and it leaves once sent`, async ({ page }) => {
    // Arrange: a board that answers a Send only when told to.
    const { name } = await openInTurn(page, who);
    let land = async () => {};
    await page.route("**/api/actions", (route: Route) => new Promise<void>((answered) => {
      land = async () => { await route.fulfill({ status: 202, json: { status: "queued" } }); answered(); };
    }));
    await field(page, name).fill("stop and rebase");
    await field(page, name).press("Enter");
    await expect(box(page, name).getByRole("button", { name: "Sending", exact: true })).toBeDisabled();

    // Act and Assert: it stays while the Send lands, and leaves once the
    // board has the message.
    await pickStyle(page, "Terminal");
    await expect(terminalInput(page)).toBeVisible();
    await expect(box(page, name)).toBeVisible();
    await land();
    await expect(box(page, name)).toBeHidden();
    await expect(page.locator(".classic-queue")).toBeHidden();
  });

  test(`a Send from the ${who}'s box that failed keeps it under the Classic terminal with its words, and it leaves once he clears them`, async ({ page }) => {
    // Arrange
    const { name } = await openInTurn(page, who);
    await page.route("**/api/actions", (route: Route) => route.fulfill({ status: 503, json: { error: "The message could not be queued. Try again." } }));
    await field(page, name).fill("stop and rebase");
    await field(page, name).press("Enter");
    await expect(refusal(page, name)).toHaveText("Not sent. The message could not be queued. Try again.");

    // Act and Assert: his words wait under the terminal to be sent again,
    // and the box leaves once he has cleared them.
    await pickStyle(page, "Terminal");
    await expect(terminalInput(page)).toBeVisible();
    await expect(field(page, name)).toHaveValue("stop and rebase");
    await field(page, name).fill("");
    await expect(box(page, name)).toBeHidden();
    await expect(page.locator(".classic-queue")).toBeHidden();
  });
}
