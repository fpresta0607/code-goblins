import { expect, openTaskList, test, type Locator } from "./site";
import { card, NAME, open, openGoblin } from "./session-board";

// The Overlord, 2026-10-27: "i dont need a stream animation for the goblin
// panels working, can just be blue dot as before". A working goblin's card
// and its panel's Task view show the plain blue status dot and nothing that moves;
// the animated working indicator stays only in Modern Chat's working spot.
// The tree row's dot is checked in baby-goblin-panel.spec.ts.

const BLUE = "rgb(74, 163, 255)";
// look is what a status shows: its dot's colour, whether an animated
// indicator is in it, and how many animations run in it.
const look = (status: Locator) => status.evaluate((element) => ({
  dot: getComputedStyle(element.querySelector(".status-dot")!).backgroundColor,
  hasIndicator: !!element.querySelector(".working-indicator"),
  animations: element.getAnimations({ subtree: true }).length,
}));

test("a working goblin's card and its panel's Task view show a still blue dot", async ({ page }) => {
  // Arrange
  await open(page);

  // Act
  const onCard = await look(card(page, NAME).locator(".plain-status"));
  await openGoblin(page);
  const onPanel = await look((await openTaskList(page)).locator(".panel-status"));

  // Assert
  for (const [where, seen] of [["card", onCard], ["panel's Task view", onPanel]] as const) {
    expect(seen, where).toEqual({ dot: BLUE, hasIndicator: false, animations: 0 });
  }
});
