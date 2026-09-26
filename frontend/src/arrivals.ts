import type { Item } from "./commandQueue.ts";

// A new question opens the Command Center on its own. Every other new item, a
// goblin's review or a command to run, is announced once in a banner so the
// Overlord cannot miss it, and stays under the badge after the banner goes.
export function bannerItems(waiting: Item[], announced: ReadonlySet<string>): Item[] {
  return waiting.filter((item) => item.kind !== "question" && !announced.has(item.key));
}

// The browser tab says how many items wait, so a board in a background tab
// still shows them.
export function countedTitle(base: string, count: number): string {
  return count ? `(${count}) ${base}` : base;
}
