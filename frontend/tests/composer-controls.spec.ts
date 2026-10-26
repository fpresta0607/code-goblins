import { expect, test } from "./site";
import { box, field, GOBLIN, NAME, open, openGoblin, pasted, snapshot, typed } from "./session-board";

for (const who of ["CFO", "goblin"] as const) {
  for (const width of [1440, 390]) {
    test(`Codex ${who} composer controls add a file, open native controls and change style at ${width}px`, async ({ page }) => {
      // Arrange
      await page.setViewportSize({ width, height: 900 });
      const relay = await open(page, snapshot({ cfo_harness: "codex" }, { harness: "codex" }));
      const name = who === "CFO" ? "CFO" : NAME;
      const kept = "C:/session-files/example.txt";
      const uploads: { task: string; name: string; text: string }[] = [];
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

      // Act: the native control button and Back preserve the draft.
      await composer.getByRole("button", { name: "Terminal controls", exact: true }).click();
      await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeFocused();
      await page.getByRole("button", { name: "Back to messages", exact: true }).click();
      await expect(field(page, name)).toHaveValue(kept);

      // Act: Classic opens the terminal; returning allows Modern again.
      await composer.getByRole("button", { name: "Style: modern", exact: true }).click();
      await page.getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Classic", exact: true }).click();
      await expect(page.getByRole("textbox", { name: "Terminal input", exact: true })).toBeVisible();
      expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("classic");
      await page.getByRole("button", { name: "Back to messages", exact: true }).click();
      await composer.getByRole("button", { name: "Style: classic", exact: true }).click();
      await page.getByRole("group", { name: "Chat style", exact: true }).getByRole("button", { name: "Modern", exact: true }).click();

      // Assert: changing views types nothing; Send submits the file once.
      await expect(field(page, name)).toHaveValue(kept);
      await expect(field(page, name)).toBeInViewport();
      expect(await page.evaluate(() => localStorage.getItem("cfo-chat-style"))).toBe("modern");
      expect(typed(relay)).toBe("");
      await composer.getByRole("button", { name: "Send", exact: true }).click();
      await expect.poll(() => typed(relay)).toBe(pasted(kept) + "\r");
      await expect(field(page, name)).toHaveValue("");
    });
  }
}
