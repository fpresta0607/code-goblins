import { test } from "./site";
import { heldPictureAfterReset } from "./conversation-picture";

test("a virtualized old picture and viewer keep their displayed conversation", async ({ page }) => {
  await heldPictureAfterReset(page, false);
});
