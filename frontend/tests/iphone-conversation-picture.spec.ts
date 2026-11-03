import { devices, servePages, test } from "./site";
import { heldPictureAfterReset } from "./conversation-picture";

const PRIVATE = "https://pc.example.ts.net:4311";
test.use({ ...devices["iPhone 15"], browserName: "webkit", baseURL: PRIVATE });

test("a virtualized old picture and viewer keep their displayed conversation on iPhone", async ({ page }) => {
  await servePages(page.context(), PRIVATE);
  await heldPictureAfterReset(page, true);
});
