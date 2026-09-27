import { useLayoutEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent } from "react";
import { dropIndex, moveTo, stepped } from "./priority";

interface Point { x: number; y: number }
interface Drag { id: string; node: HTMLElement; start: Point; startAt: Point; pointer: Point; moved: boolean }

const ITEMS = ":scope > [data-sort-id]";
const same = (a: string[], b: string[]) => a.length === b.length && a.every((id, index) => b[index] === id);
// Where the list lays a card out, apart from any transform on it.
const at = (node: HTMLElement): Point => ({ x: node.offsetLeft, y: node.offsetTop });

// A list whose cards the Overlord orders by dragging, or with Alt and an
// arrow key on a focused card. While a card is dragged the others make room
// and slide to their new places; onOrder gets the order it was dropped in.
// A mouse or pen drags a card from anywhere on it, and a finger from its rank,
// so a finger on the rest of the card still scrolls the page.
export function useSortable(ids: string[], onOrder: (order: string[], moved: string) => void) {
  const listRef = useRef<HTMLDivElement>(null);
  const [preview, setPreview] = useState<string[] | null>(null);
  const order = preview ?? ids;
  const shown = useRef(order);
  const drag = useRef<Drag | null>(null);
  const places = useRef(new Map<string, Point>());
  const focusAfter = useRef<string | null>(null);
  const dropped = useRef(false);

  // The dragged card follows the pointer from wherever the list lays it out.
  const place = () => {
    const current = drag.current;
    if (!current?.moved) return;
    const now = at(current.node);
    current.node.style.transform = `translate(${current.pointer.x - current.start.x - (now.x - current.startAt.x)}px, ${current.pointer.y - current.start.y - (now.y - current.startAt.y)}px)`;
  };

  useLayoutEffect(() => {
    const list = listRef.current;
    const moved = !same(shown.current, order);
    shown.current = order;
    if (!list) return;
    const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
    const next = new Map<string, Point>();
    for (const node of list.querySelectorAll<HTMLElement>(ITEMS)) {
      const id = node.dataset.sortId || "";
      const before = places.current.get(id), now = at(node);
      next.set(id, now);
      // FLIP: a card the order moved slides from where it was.
      if (moved && !still && before && (before.x !== now.x || before.y !== now.y) && id !== drag.current?.id) {
        node.animate([{ transform: `translate(${before.x - now.x}px, ${before.y - now.y}px)` }, { transform: "none" }], { duration: 200, easing: "cubic-bezier(.2,.8,.2,1)" });
      }
    }
    places.current = next;
    place();
    if (focusAfter.current) {
      list.querySelector<HTMLElement>(`[data-sort-id="${CSS.escape(focusAfter.current)}"] .task-card`)?.focus();
      focusAfter.current = null;
    }
  });

  const onPointerDown = (event: PointerEvent<HTMLElement>) => {
    const list = listRef.current;
    const target = event.target as HTMLElement;
    const control = target.closest("a, button, input, textarea");
    if (!list || event.button !== 0 || shown.current.length < 2 || control && !control.classList.contains("task-card")) return;
    if (event.pointerType === "touch" && !target.closest(".rank")) return;
    const node = event.currentTarget;
    const id = node.dataset.sortId || "";
    const start = { x: event.clientX, y: event.clientY };
    drag.current = { id, node, start, startAt: at(node), pointer: start, moved: false };
    const move = (next: globalThis.PointerEvent) => {
      const current = drag.current;
      if (!current) return;
      current.pointer = { x: next.clientX, y: next.clientY };
      if (!current.moved) {
        if (Math.hypot(next.clientX - current.start.x, next.clientY - current.start.y) < 5) return;
        current.moved = true;
        node.classList.add("dragging");
        list.classList.add("sorting");
      }
      next.preventDefault();
      const others = [...list.querySelectorAll<HTMLElement>(ITEMS)].filter((other) => other !== node);
      const frame = list.getBoundingClientRect();
      const columns = getComputedStyle(list).gridTemplateColumns.split(" ").length;
      const index = dropIndex(others.map((other) => ({ left: other.offsetLeft, top: other.offsetTop, width: other.offsetWidth, height: other.offsetHeight })), next.clientX - frame.left, next.clientY - frame.top, columns);
      const moved = moveTo(shown.current, id, index);
      if (same(moved, shown.current)) place();
      else setPreview(moved);
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
      const current = drag.current;
      drag.current = null;
      if (!current?.moved) return;
      node.classList.remove("dragging");
      list.classList.remove("sorting");
      // The card settles into its place instead of jumping there.
      const from = node.style.transform;
      node.style.transform = "";
      if (!matchMedia("(prefers-reduced-motion: reduce)").matches) node.animate([{ transform: from }, { transform: "none" }], { duration: 180, easing: "cubic-bezier(.2,.8,.2,1)" });
      // The click that ends a drag does not open the card.
      dropped.current = true;
      setTimeout(() => { dropped.current = false; });
      const final = shown.current;
      setPreview(null);
      if (!same(final, ids)) onOrder(final, current.id);
    };
    window.addEventListener("pointermove", move, { passive: false });
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (!event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
    event.preventDefault();
    const id = event.currentTarget.dataset.sortId || "";
    const next = stepped(ids, id, event.key === "ArrowUp" ? -1 : 1);
    if (!next) return;
    // Reordering moves the card's element, which drops its focus.
    focusAfter.current = id;
    onOrder(next, id);
  };

  const onClickCapture = (event: MouseEvent<HTMLElement>) => {
    if (!dropped.current) return;
    event.preventDefault();
    event.stopPropagation();
  };

  // Each card of the list carries data-sort-id and these three handlers.
  return { listRef, order, onPointerDown, onKeyDown, onClickCapture };
}
