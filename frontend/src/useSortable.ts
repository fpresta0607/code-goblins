import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent, type RefObject } from "react";
import { dropIndex, edgeScroll, moveTo, stepped } from "./priority";

interface Point { x: number; y: number }
// origin is where the list was on the screen when the drag began.
interface Drag { id: string; node: HTMLElement; start: Point; startAt: Point; origin: Point; pointer: Point; moved: boolean }
// A paged list's pager, and carry, which turns the page under a dragged card
// and answers with the place the card takes on it, or null when there is no
// such page.
export interface PageTurn { pager: RefObject<HTMLElement | null>; carry: (step: -1 | 1) => number | null }

// How long a dragged card rests on a page arrow before the page turns, and
// before it turns again.
const ARROW_HOLD = 600;
const ITEMS = ":scope > [data-sort-id]";
const same = (a: string[], b: string[]) => a.length === b.length && a.every((id, index) => b[index] === id);
// Where the list lays a card out, apart from any transform on it.
const at = (node: HTMLElement): Point => ({ x: node.offsetLeft, y: node.offsetTop });
// What scrolls a list up and down: its nearest ancestor that does, or the page.
function scrollerOf(list: HTMLElement): Element {
  for (let node = list.parentElement; node; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY;
    if ((overflow === "auto" || overflow === "scroll") && node.scrollHeight > node.clientHeight) return node;
  }
  return document.documentElement;
}
// The top and bottom of a scroller as the screen shows them.
function edgesOf(scroller: Element): { top: number; bottom: number } {
  if (scroller === document.documentElement) return { top: 0, bottom: innerHeight };
  const box = scroller.getBoundingClientRect();
  return { top: Math.max(0, box.top), bottom: Math.min(innerHeight, box.bottom) };
}

// A list whose cards the Overlord orders by dragging, or with Alt and an
// arrow key on a focused card. While a card is dragged the others make room
// and slide to their new places; onOrder gets the order it was dropped in.
// A mouse or pen drags a card from anywhere on it, and a finger from its rank,
// so a finger on the rest of the card still scrolls the page. A card reaches
// any place in its list: held at the top or bottom edge of what scrolls the
// list, it waits while the list scrolls under it, and held over a page arrow
// it turns the page and goes with it. A list shown a page at a time renders
// its cards from offset on, and a drag places a card among them; listRef is
// the list the cards are the children of.
export function useSortable(ids: string[], onOrder: (order: string[], moved: string) => void, offset: number, listRef: RefObject<HTMLDivElement | null>, pages: PageTurn) {
  const [preview, setPreview] = useState<string[] | null>(null);
  const order = preview ?? ids;
  const shown = useRef(order);
  const drag = useRef<Drag | null>(null);
  const places = useRef(new Map<string, Point>());
  const focusAfter = useRef<string | null>(null);
  const dropped = useRef(false);
  // The page shown now, which a drag begun on an earlier page reads.
  const live = useRef({ offset, pages });
  // Ends a drag in progress; a list that goes away mid-drag ends its drag.
  const release = useRef<(() => void) | null>(null);
  useEffect(() => () => release.current?.(), []);

  // The dragged card follows the pointer from wherever the list lays it out
  // and wherever the list has scrolled to.
  const place = () => {
    const current = drag.current, list = listRef.current;
    if (!current?.moved || !list) return;
    const now = at(current.node), frame = list.getBoundingClientRect();
    current.node.style.transform = `translate(${current.pointer.x - current.start.x - (now.x - current.startAt.x) - (frame.left - current.origin.x)}px, ${current.pointer.y - current.start.y - (now.y - current.startAt.y) - (frame.top - current.origin.y)}px)`;
  };

  useLayoutEffect(() => {
    const list = listRef.current;
    const moved = !same(shown.current, order);
    shown.current = order;
    live.current = { offset, pages };
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
    const origin = list.getBoundingClientRect();
    const dragged: Drag = { id, node, start, startAt: at(node), origin: { x: origin.left, y: origin.top }, pointer: start, moved: false };
    drag.current = dragged;
    const scroller = scrollerOf(list);
    let scrolling = 0, turning = 0, held: -1 | 0 | 1 = 0;
    // Puts the card among the others where the pointer holds it.
    const settle = () => {
      const others = [...list.querySelectorAll<HTMLElement>(ITEMS)].filter((other) => other !== node);
      const frame = list.getBoundingClientRect();
      const columns = getComputedStyle(list).gridTemplateColumns.split(" ").length;
      const index = dropIndex(others.map((other) => ({ left: other.offsetLeft, top: other.offsetTop, width: other.offsetWidth, height: other.offsetHeight })), dragged.pointer.x - frame.left, dragged.pointer.y - frame.top, columns);
      const moved = moveTo(shown.current, id, live.current.offset + index);
      if (same(moved, shown.current)) place();
      else setPreview(moved);
    };
    // The page arrow the pointer is on: -1 for earlier, 1 for next, 0 for none.
    const arrow = (): -1 | 0 | 1 => {
      for (const button of live.current.pages.pager.current?.querySelectorAll<HTMLButtonElement>("[data-turn]") ?? []) {
        const box = button.getBoundingClientRect();
        if (!button.disabled && dragged.pointer.x >= box.left && dragged.pointer.x <= box.right && dragged.pointer.y >= box.top && dragged.pointer.y <= box.bottom) return button.dataset.turn === "1" ? 1 : -1;
      }
      return 0;
    };
    // Turns the page under a card that rested on its arrow, and again for as
    // long as it rests there.
    const turn = () => {
      held = arrow();
      if (!held) return;
      const taken = live.current.pages.carry(held);
      if (taken !== null) setPreview(moveTo(shown.current, id, taken));
      turning = window.setTimeout(turn, ARROW_HOLD);
    };
    // Starts the wait on a page arrow the pointer has come onto, whether the
    // pointer moved there or the list scrolled the arrow under it, and ends
    // the wait on one it has left.
    const rest = () => {
      const over = arrow();
      if (over === held) return;
      clearTimeout(turning);
      held = over;
      if (over) turning = window.setTimeout(turn, ARROW_HOLD);
    };
    // Scrolls the list a step each frame while the card is held at an edge
    // and there is more to scroll, and asks for no frame once there is not.
    const scroll = () => {
      scrolling = 0;
      const { top, bottom } = edgesOf(scroller), before = scroller.scrollTop;
      scroller.scrollTop += edgeScroll(dragged.pointer.y, top, bottom);
      if (scroller.scrollTop === before) return;
      settle();
      rest();
      scrolling = requestAnimationFrame(scroll);
    };
    const move = (next: globalThis.PointerEvent) => {
      dragged.pointer = { x: next.clientX, y: next.clientY };
      if (!dragged.moved) {
        if (Math.hypot(next.clientX - start.x, next.clientY - start.y) < 5) return;
        dragged.moved = true;
        node.classList.add("dragging");
        list.classList.add("sorting");
      }
      next.preventDefault();
      settle();
      const { top, bottom } = edgesOf(scroller);
      if (!scrolling && edgeScroll(dragged.pointer.y, top, bottom)) scrolling = requestAnimationFrame(scroll);
      rest();
    };
    const detach = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", cancel);
      cancelAnimationFrame(scrolling);
      clearTimeout(turning);
      release.current = null;
    };
    // A gesture the browser or system cancels puts the card back.
    const cancel = () => {
      detach();
      const current = drag.current;
      drag.current = null;
      if (!current?.moved) return;
      node.classList.remove("dragging");
      list.classList.remove("sorting");
      node.style.transform = "";
      setPreview(null);
    };
    const up = () => {
      detach();
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
    window.addEventListener("pointercancel", cancel);
    release.current = cancel;
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
  return { order, onPointerDown, onKeyDown, onClickCapture };
}
