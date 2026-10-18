import { expect, holdStream, test, type Page } from "./site";

const URL = "https://my-desktop.example.ts.net:4311";
const OFF = { enabled: false, ready: false, scrawl_ready: false };
const ON = { enabled: true, ready: true, url: URL, login: "owner@example.com", scrawl_ready: false };

async function open(page: Page, fail = false) {
  const actions: unknown[] = [];
  let status: typeof OFF | typeof ON = OFF;
  await holdStream(page, { healthy: true, instance: "fixture", revision: 1, attention: [], tasks: [], afk: { state: "off" }, cfo_runs: true, cfo_harness: "claude", cfo_terminal: "cfo", cfo_terminal_since: "2026-10-08T09:00:00Z", sessions: [{ id: "cfo-1", harness: "claude", role: "cfo" }] });
  await page.route("**/api/**", (route) => route.fulfill({ json: {} }));
  await page.route("**/api/remote", (route) => {
    if (route.request().method() === "POST") {
      const body: { on?: boolean; scrawl?: boolean } = route.request().postDataJSON();
      actions.push(body);
      expect(route.request().headers()["x-cfo-token"]).toBe("fixture");
      if (fail) return route.fulfill({ status: 409, json: { error: "Connect Tailscale on this PC before enabling remote access." } });
      status = body.scrawl ? { ...ON, scrawl_ready: true } : body.on ? ON : OFF;
    }
    return route.fulfill({ json: status });
  });
  await page.goto("/");
  await page.locator(".cfo-pin").getByRole("button", { name: "Open the CFO's terminal" }).last().click();
  await page.locator(".panel-pill").getByRole("button", { name: "Task", exact: true }).click();
  await page.locator(".disclosure.remote-access > summary").click();
  return { actions, panel: page.getByRole("region", { name: "Remote access" }) };
}

test.use({ viewport: { width: 390, height: 844 } });

test("the CFO task panel enables private phone access, copies its link and turns it off", async ({ page }, testInfo) => {
  const { actions, panel } = await open(page);
  await expect(panel.getByRole("button", { name: "Enable", exact: true })).toBeVisible();
  for (const [name, href] of [["For iPhone", "https://tailscale.com/download/ios"], ["For Android", "https://tailscale.com/download/android"]]) await expect(panel.getByRole("link", { name, exact: true })).toHaveAttribute("href", href);
  await panel.getByRole("button", { name: "Enable", exact: true }).click();
  await expect(panel.getByText("Private access is ready.")).toBeVisible();
  await expect(panel.getByRole("link", { name: "Open private board" })).toHaveAttribute("href", URL);
  await page.evaluate(() => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async (text: string) => { document.documentElement.dataset.copied = text; } } });
  });
  await panel.getByRole("button", { name: "Copy " + URL }).click();
  await expect(page.locator("html")).toHaveAttribute("data-copied", URL);
  await expect(panel.getByText(/Add to Home Screen/)).toBeVisible();
  await panel.getByRole("button", { name: "Set up Scrawl", exact: true }).click();
  await expect(panel.getByText("Private reviews are ready. Open a review from this board.")).toBeVisible();
  await panel.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(panel.getByText("Private reviews are ready. Open a review from this board.")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await panel.screenshot({ path: testInfo.outputPath("remote-access-phone.png") });
  await panel.getByRole("button", { name: "Turn off", exact: true }).click();
  await expect(panel.getByRole("button", { name: "Enable", exact: true })).toBeVisible();
  await expect(panel.getByRole("link", { name: "Open private board" })).toHaveCount(0);
  expect(actions).toEqual([{ on: true }, { scrawl: true }, { on: false }]);
});

test("setup failures name the next step without pretending remote access is ready", async ({ page }) => {
  const { panel } = await open(page, true);
  await panel.getByRole("button", { name: "Enable", exact: true }).click();
  await expect(panel.locator(".click-feedback")).toHaveText("Connect Tailscale on this PC before enabling remote access.");
  await expect(panel.getByRole("link", { name: "Open private board" })).toHaveCount(0);
});
