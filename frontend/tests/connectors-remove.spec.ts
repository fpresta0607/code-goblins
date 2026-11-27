import { fleet, item, NOW, play, type Supervisor } from "./connectors-supervisor";
import { expect, test, type Page } from "./site";

// Remove on a card of Needs a look (#3002): it asks first, naming what will
// change; Cancel changes nothing; a removal posts the one item and the item
// leaves the list; a refusal says why in the same box and the item stays; and
// an item Code Goblins does not declare says where it lives with only Close.
// The page stands alone in the board's canvas, and the supervisor is played.
async function open(page: Page, supervisor: Supervisor) {
  await page.clock.install({ time: Date.parse(NOW) });
  await play(page, supervisor);
  await page.goto("/tests/fixtures/connectors-page.html");
  await expect(page.locator(".look-card").first()).toBeVisible();
}

const attention = (page: Page) => page.getByRole("region", { name: "Needs a look", exact: true });
const names = (page: Page) => attention(page).locator(".connector-open");
const ALL = ["gcloud", "ACME_API_KEY", "Supabase", "Acme Docs"];

test("Cancel closes the question with nothing posted and the card where it was, its Remove focused again", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  await open(page, supervisor);
  const remove = attention(page).getByRole("button", { name: "Remove ACME_API_KEY, This PC" });

  // Act
  await remove.click();
  const confirm = page.getByRole("dialog", { name: "Remove ACME_API_KEY?" });

  // Assert: it names what every program loses and where the value is kept.
  await expect(confirm).toContainText("Deletes ACME_API_KEY from this PC's user environment, so every program started from now on no longer gets it");
  await expect(confirm).toContainText("such as a server in your Claude settings");
  await expect(confirm).toContainText("Its value is first kept in the credential vault under ACME_API_KEY");
  await expect(confirm.getByRole("button", { name: "Cancel" })).toBeFocused();

  // Act
  await confirm.getByRole("button", { name: "Cancel" }).click();

  // Assert
  await expect(confirm).toBeHidden();
  await expect(names(page)).toHaveText(ALL);
  await expect(remove).toBeFocused();
  expect(supervisor.posts).toEqual([]);
});

test("Remove of a goblin's server posts its goblin and generation, and the item leaves the list", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  await open(page, supervisor);

  // Act
  await attention(page).getByRole("button", { name: "Remove Acme Docs, Zeke - Touch Wrangler in acme" }).click();
  const confirm = page.getByRole("dialog", { name: "Remove Acme Docs?" });
  await expect(confirm).toContainText("Takes acme-docs out of the task record of Zeke - Touch Wrangler in acme");
  await confirm.getByRole("button", { name: "Remove", exact: true }).click();

  // Assert: focus lands on the list's heading, since the card it came from is gone.
  await expect(confirm).toBeHidden();
  await expect(names(page)).toHaveText(["gcloud", "ACME_API_KEY", "Supabase"]);
  await expect(attention(page).getByRole("heading", { name: /^Needs a look/ })).toBeFocused();
  expect(supervisor.posts.map((post) => [post.path, post.token, post.body])).toEqual([
    ["/api/connectors/remove", "connectors-proof", { level: "goblin", project: "acme", task: "cg-touch", generation: "cg-touch-1", connection: "failed:acme-docs" }],
  ]);
});

test("Remove of a goblin's server still starting names its task record and posts the pending item", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.view.goblins[0].items.push(item("goblin", "loaded", "mcp", "acme-wiki", "not_checked", { id: "pending:acme-wiki", source: "its session record" }));
  await open(page, supervisor);

  // Act
  await attention(page).getByRole("button", { name: "Remove Acme Wiki, Zeke - Touch Wrangler in acme" }).click();
  const confirm = page.getByRole("dialog", { name: "Remove Acme Wiki?" });
  await expect(confirm).toContainText("Takes acme-wiki out of the task record of Zeke - Touch Wrangler in acme");
  await confirm.getByRole("button", { name: "Remove", exact: true }).click();

  // Assert
  await expect(confirm).toBeHidden();
  await expect(names(page)).toHaveText(ALL);
  expect(supervisor.posts.map((post) => [post.path, post.body])).toEqual([
    ["/api/connectors/remove", { level: "goblin", project: "acme", task: "cg-touch", generation: "cg-touch-1", connection: "pending:acme-wiki" }],
  ]);
});

test("a removal that empties Needs a look moves focus to the line that says so", async ({ page }) => {
  // Arrange: Acme Docs is the only item in trouble.
  const supervisor = fleet();
  supervisor.view.machine.items = supervisor.view.machine.items.filter((each) => each.status !== "not_checked" && each.status !== "everywhere");
  supervisor.view.projects = supervisor.view.projects.filter((project) => project.name !== "globex");
  await open(page, supervisor);

  // Act
  await attention(page).getByRole("button", { name: "Remove Acme Docs, Zeke - Touch Wrangler in acme" }).click();
  await page.getByRole("dialog", { name: "Remove Acme Docs?" }).getByRole("button", { name: "Remove", exact: true }).click();

  // Assert
  await expect(attention(page)).toBeHidden();
  await expect(page.getByText("Nothing needs a look.")).toBeFocused();
});

test("a Remove the supervisor refuses says why in the question, with nothing changed, and the card stays", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  supervisor.refuses = "The vault already holds another value under ACME_API_KEY, so removing it here would lose this one. Nothing was removed.";
  await open(page, supervisor);

  // Act
  await attention(page).getByRole("button", { name: "Remove ACME_API_KEY, This PC" }).click();
  const confirm = page.getByRole("dialog", { name: "Remove ACME_API_KEY?" });
  await confirm.getByRole("button", { name: "Remove", exact: true }).click();

  // Assert
  await expect(confirm.getByRole("alert")).toHaveText("Nothing changed: " + supervisor.refuses);
  await expect(confirm.getByRole("button")).toHaveText(["Close"]);
  await confirm.getByRole("button", { name: "Close" }).click();
  await expect(confirm).toBeHidden();
  await expect(names(page)).toHaveText(ALL);
  expect(supervisor.posts.map((post) => post.path)).toEqual(["/api/connectors/remove"]);
});

test("an item Code Goblins does not declare says where it lives with only Close, and posts nothing", async ({ page }) => {
  // Arrange
  const supervisor = fleet();
  await open(page, supervisor);

  // Act
  await attention(page).getByRole("button", { name: "Remove gcloud, This PC" }).click();
  const confirm = page.getByRole("dialog", { name: "Remove gcloud?" });

  // Assert
  await expect(confirm).toContainText("Code Goblins does not declare gcloud");
  await expect(confirm.getByRole("button")).toHaveText(["Close"]);
  await confirm.getByRole("button", { name: "Close" }).click();
  await expect(names(page)).toHaveText(ALL);
  expect(supervisor.posts).toEqual([]);
});
