import { expect, test } from "./site";
import { box, boxMenu, field, GOBLIN, NAME, open, openGoblin, pasted, snapshot, typed } from "./session-board";

for (const who of ["CFO", "goblin"] as const) {
  for (const width of [1440, 390]) {
    test(`Codex ${who} composer controls add a file, open a terminal window and change style at ${width}px`, async ({ page }) => {
      // Arrange
      await page.setViewportSize({ width, height: 900 });
      const relay = await open(page, snapshot({ cfo_harness: "codex" }, { harness: "codex" }));
      const name = who === "CFO" ? "CFO" : NAME;
      const kept = "C:/session-files/example.txt";
      const uploads: { task: string; name: string; text: string }[] = [];
      const windows: unknown[] = [];
      await page.route("**/api/terminal/open", (route) => { windows.push(route.request().postDataJSON()); return route.fulfill({ json: {} }); });
      await page.route("**/api/composer/file**", async (route) => {
        const query = new URL(route.request().url()).searchParams;
        uploads.push({ task: query.get("task") ?? "", name: query.get("name") ?? "", text: route.request().postDataBuffer()?.toString() ?? "" });
        await route.fulfill({ status: 201, json: { path: kept } });
      });
      if (who === "CFO") await page.keyboard.press("Control+Alt+1");
      else await openGoblin(page);
      const composer = box(page, name);

      // Act: the visible plus opens the chooser and keeps the chosen file.
      const choosing = page.waitForEvent("filechooser");
      await composer.getByRole("button", { name: "Add files", exact: true }).click();
      await (await choosing).setFiles({ name: "example.txt", mimeType: "text/plain", buffer: Buffer.from("read this attachment") });

      // Assert
      await expect(field(page, name)).toHaveValue(kept);
      expect(uploads).toEqual([{ task: who === "CFO" ? "" : GOBLIN.id, name: "example.txt", text: "read this attachment" }]);
      expect(typed(relay)).toBe("");

      // Act: opening a separate terminal preserves the draft and view.
      await (await boxMenu(page, name)).getByRole("button", { name: "Open in Windows Terminal", exact: true }).click();
      await expect.poll(() => windows).toEqual([who === "CFO" ? { native: "cfo=cfo-term" } : { native: `task=${GOBLIN.id}&generation=${GOBLIN.generation}` }]);
      await expect(field(page, name)).toHaveValue(kept);

      // Act: Classic opens the terminal; Back to messages itself chooses Modern
      // again, as it saves Modern while it steps out.
      await (await boxMenu(page, name)).getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Classic", exact: true }).click();
      await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
      expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("classic");
      await page.getByRole("button", { name: "Back to messages", exact: true }).click();
      await expect(composer.getByRole("button", { name: "Style: modern", exact: true })).toBeVisible();
      expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style")), "Back to messages chose Modern").toBe("modern");

      // Assert: changing views types nothing; Send submits the file once.
      await expect(field(page, name)).toHaveValue(kept);
      await expect(field(page, name)).toBeInViewport();
      expect(typed(relay)).toBe("");
      await composer.getByRole("button", { name: "Send", exact: true }).click();
      await expect.poll(() => typed(relay)).toBe(pasted(kept) + "\r");
      await expect(field(page, name)).toHaveValue("");
    });
  }
}
