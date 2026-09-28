// The browser tab says how many items wait, so a board in a background tab
// still shows them.
export function countedTitle(base: string, count: number): string {
  return count ? `(${count}) ${base}` : base;
}
