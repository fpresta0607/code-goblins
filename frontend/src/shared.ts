// shared is next with every part equal to the same part of prior replaced by
// prior's own, so a part an update left as it was keeps its identity and what
// draws only that part is not drawn again. Items of a list that carry an id
// are matched by it, so a list that gained or lost an item keeps the rest.
// Each snapshot carries the whole fleet, and on 2026-10-27 one that changed
// nothing redrew 440 components (#1599).
export function shared<T>(prior: unknown, next: T): T {
  if (prior === next || typeof next !== "object" || next === null || typeof prior !== "object" || prior === null) return next;
  if (Array.isArray(next)) {
    if (!Array.isArray(prior)) return next;
    const byID = new Map<unknown, unknown>();
    for (const item of prior) if (typeof item === "object" && item !== null && typeof (item as { id?: unknown }).id === "string") byID.set((item as { id: string }).id, item);
    let isSame = prior.length === next.length;
    const kept = next.map((item: unknown, index) => {
      const id = typeof item === "object" && item !== null ? (item as { id?: unknown }).id : undefined;
      const value = shared(typeof id === "string" && byID.has(id) ? byID.get(id) : prior[index], item);
      if (value !== prior[index]) isSame = false;
      return value;
    });
    return (isSame ? prior : kept) as T;
  }
  if (Array.isArray(prior)) return next;
  const before = prior as Record<string, unknown>;
  const kept: Record<string, unknown> = {};
  let isSame = Object.keys(before).length === Object.keys(next).length;
  for (const [key, value] of Object.entries(next)) {
    kept[key] = shared(before[key], value);
    if (kept[key] !== before[key] || !(key in before)) isSame = false;
  }
  return (isSame ? prior : kept) as T;
}
