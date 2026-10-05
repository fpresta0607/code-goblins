import { expect, test, type Page } from "./site";

const problem = "State folder native-inbox cannot be read; native hook events are delayed while other board updates continue: open C:\\scratch-home\\state\\native-inbox: Access is denied.";
const snapshot = (revision: number, error: string, title: string) => ({
  instance: "same-supervisor", revision, healthy: true, cfo_runs: true, error,
  tasks: [{ id: "task", title, generation: "same-task", phase: "working", verified: false }],
});
const send = (page: Page, data: object) => page.evaluate((data) => {
  window.dispatchEvent(new CustomEvent("fixture-snapshot", { detail: data }));
}, data);

for (const width of [1440, 390]) {
  test(`the board names an unreadable folder and keeps drawing live updates at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(() => {
      class FixtureSource extends EventTarget {
        private publish = (event: Event) => {
          if (event instanceof CustomEvent) this.dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(event.detail) }));
        };
        constructor() {
          super();
          window.addEventListener("fixture-snapshot", this.publish);
        }
        close() { window.removeEventListener("fixture-snapshot", this.publish); }
      }
      Object.defineProperty(window, "EventSource", { value: FixtureSource });
    });
    await page.route("**/api/**", (route) => route.fulfill({ status: 404, json: { error: "No fixture for this resource" } }));
    await page.goto("/");
    await send(page, snapshot(1, "", "Before the folder problem"));
    await expect(page.getByText("Before the folder problem", { exact: true })).toBeVisible();
    await send(page, snapshot(2, problem, "New evidence while the folder is unreadable"));
    const warning = page.getByRole("alert").filter({ hasText: "State folder native-inbox" });
    await expect(warning).toHaveText(problem);
    await expect(page.getByText("Live", { exact: true })).toBeVisible();
    await expect(page.getByText("New evidence while the folder is unreadable", { exact: true })).toBeVisible();
    await expect(page.getByText(/Supervisor connection lost/)).toHaveCount(0);
    expect(await warning.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await send(page, snapshot(3, "", "Access restored in the same session"));
    await expect(warning).toHaveCount(0);
    await expect(page.getByText("Access restored in the same session", { exact: true })).toBeVisible();
  });
}
