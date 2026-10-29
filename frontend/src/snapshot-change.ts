import { array, object, string } from "./types.ts";

// applyChange is held with a change applied, as the supervisor's stream sends
// one after its first snapshot, carrying only what moved since it last sent
// (internal/supervisor/snapshot_change.go): a value whole, or an object's
// keys that changed and those it lost, or the items of a list that changed by
// id, with every id in order when items came, went or moved, or only the ids
// that went and those that came at its end. A change that does not fit what
// the board holds throws.
export function applyChange(held: unknown, change: unknown): unknown {
  const each = object(change);
  if ("value" in each) return each.value;
  if ("keys" in each || "gone" in each) {
    const before = object(held);
    const after: Record<string, unknown> = { ...before };
    for (const key of array(each.gone)) delete after[string(key)];
    for (const [key, value] of Object.entries(each.keys == null ? {} : object(each.keys))) after[key] = applyChange(before[key], value);
    return after;
  }
  if (!Array.isArray(held)) throw new Error("Invalid snapshot change");
  const byID = new Map(held.map((item) => [string(object(item).id), item]));
  const items = each.items == null ? {} : object(each.items);
  const removed = new Set(array(each.removed).map(string));
  const order = [...(each.order == null ? [...byID.keys()] : array(each.order).map(string)).filter((id) => !removed.has(id)), ...array(each.appended).map(string)];
  return order.map((id) => {
    if (Object.hasOwn(items, id)) return applyChange(byID.get(id), items[id]);
    if (!byID.has(id)) throw new Error("Invalid snapshot change");
    return byID.get(id);
  });
}
