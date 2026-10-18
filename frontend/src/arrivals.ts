// The browser tab says how many items wait, so a board in a background tab
// still shows them.
export function countedTitle(base: string, count: number): string {
  return count ? `(${count}) ${base}` : base;
}

// showBadge puts the same count on the app's icon where the browser badges an
// installed app, and takes it off at none. It returns the browser's answer,
// or nothing where the browser badges no app.
export function showBadge(count: number): Promise<void> | undefined {
  if (typeof navigator.setAppBadge !== "function") return undefined;
  return count ? navigator.setAppBadge(count) : navigator.clearAppBadge();
}
