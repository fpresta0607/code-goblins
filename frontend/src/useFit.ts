import { useLayoutEffect, useRef, useState, type PointerEvent, type RefObject } from "react";
import { availableHeight, clampPage, pageOf, pageStarts, swipeStep } from "./fit";

const columnsOf = (grid: HTMLElement) => {
  const value = getComputedStyle(grid).gridTemplateColumns;
  return value === "none" ? 1 : value.split(" ").length;
};

// What the list's layout gave at its current width: the space it may fill,
// the gap and columns of its grid, and each card's height by key.
interface Layout { width: number; available: number; gap: number; columns: number; heights: Map<string, number> }

const sameLayout = (a: Layout, b: Layout) => a.width === b.width && a.available === b.available && a.gap === b.gap && a.columns === b.columns
  && a.heights.size === b.heights.size && [...a.heights].every(([key, height]) => b.heights.get(key) === height);

// Fits a list's cards, keys in order, to the board's visible canvas: each page
// holds as many of its own cards as fit below the list (see availableHeight
// and pageStarts), and a sideways swipe turns it. Every card inside the frame
// that carries its key in data-fit-key is measured, so the caller also renders
// the cards in unmeasured, those not measured yet at this width, in a hidden
// container of no height beside the list: every page is then sized by its
// cards' real heights without the list ever growing past its space. Until a
// card is measured it counts as tall as the tallest one that was, and before
// any is, a page holds one card. frameRef is the box around the list, listRef
// the list itself.
export function useFit(keys: string[], frameRef: RefObject<HTMLDivElement | null>, listRef: RefObject<HTMLDivElement | null>) {
  const [layout, setLayout] = useState<Layout>({ width: -1, available: 0, gap: 0, columns: 1, heights: new Map() });
  const [chosen, setChosen] = useState(0);
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const tallest = layout.heights.size ? Math.max(...layout.heights.values()) : Number.POSITIVE_INFINITY;
  const heightsOf = (order: string[]) => order.map((key) => layout.heights.get(key) ?? tallest);
  const starts = pageStarts(heightsOf(keys), layout.available, layout.gap, layout.columns);
  const page = clampPage(chosen, starts.length);
  const start = starts[page];
  const end = starts[page + 1] ?? keys.length;
  const unmeasured = keys.filter((key) => !layout.heights.has(key));
  // A measure is due whenever the shown or the unmeasured cards change.
  const watched = [...keys.slice(start, end), "", ...unmeasured].join("\n");

  useLayoutEffect(() => {
    const frame = frameRef.current, list = listRef.current;
    if (!frame || !list) return;
    const canvas = frame.closest<HTMLElement>(".canvas-region") ?? document.documentElement;
    const board = frame.closest<HTMLElement>(".task-board");
    const column = frame.closest<HTMLElement>(".board-column") ?? frame;
    const measure = () => {
      const frameTop = frame.getBoundingClientRect().top;
      const reserve = 48 + (parseFloat(getComputedStyle(column).paddingBottom) || 0) + (board ? parseFloat(getComputedStyle(board).paddingBottom) || 0 : 0);
      // On a narrow screen the canvas grows with the board and the page
      // scrolls, so the window is what is visible.
      const available = availableHeight({
        view: Math.min(canvas.clientHeight, window.innerHeight),
        top: frameTop - canvas.getBoundingClientRect().top + canvas.scrollTop,
        heading: frameTop - column.getBoundingClientRect().top,
        stacked: !board || columnsOf(board) === 1,
        reserve,
      });
      const width = list.clientWidth;
      setLayout((prior) => {
        const heights = new Map(prior.width === width ? prior.heights : []);
        for (const card of frame.querySelectorAll<HTMLElement>("[data-fit-key]")) heights.set(card.dataset.fitKey || "", card.offsetHeight);
        const next = { width, available, gap: parseFloat(getComputedStyle(list).rowGap) || 0, columns: columnsOf(list), heights };
        return sameLayout(prior, next) ? prior : next;
      });
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(canvas);
    observer.observe(list);
    if (board) observer.observe(board);
    window.addEventListener("resize", measure);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [watched, frameRef, listRef]);

  const show = (next: number) => setChosen(clampPage(next, starts.length));
  const turn = (step: number) => show(page + step);
  const onPointerDown = (event: PointerEvent<HTMLElement>) => {
    swipe.current = event.pointerType === "touch" ? { x: event.clientX, y: event.clientY } : null;
  };
  const onPointerUp = (event: PointerEvent<HTMLElement>) => {
    const begun = swipe.current;
    swipe.current = null;
    if (begun) turn(swipeStep(event.clientX - begun.x, event.clientY - begun.y));
  };
  const onPointerCancel = () => {
    swipe.current = null;
  };
  // pageAt is the page that holds the card at index when the list is in
  // order, such as the order a move is about to save.
  const pageAt = (order: string[], index: number) => pageOf(index, pageStarts(heightsOf(order), layout.available, layout.gap, layout.columns));
  return { start, end, page, unmeasured, pageAt, turn, show, onPointerDown, onPointerUp, onPointerCancel };
}
