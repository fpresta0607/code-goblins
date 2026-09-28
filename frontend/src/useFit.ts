import { useLayoutEffect, useRef, useState, type PointerEvent, type RefObject } from "react";
import { availableHeight, clampPage, listPageStarts, pageShowing, swipeStep } from "./fit";

const columnsOf = (grid: HTMLElement) => {
  const value = getComputedStyle(grid).gridTemplateColumns;
  return value === "none" ? 1 : value.split(" ").length;
};

// What the list's layout gave at its current width: the space it may fill,
// the gap and columns of its grid, and each card's height by key.
interface Layout { width: number; available: number; gap: number; columns: number; heights: Map<string, number> }

const sameLayout = (a: Layout, b: Layout) => a.width === b.width && a.available === b.available && a.gap === b.gap && a.columns === b.columns
  && a.heights.size === b.heights.size && [...a.heights].every(([key, height]) => b.heights.get(key) === height);

// Fits a list's cards, keys in order, to the board's visible canvas: a list of
// up to ten shows whole, and past that each page holds as many of its own
// cards as fit below the list (see availableHeight and listPageStarts), and a
// sideways swipe turns it. Every card inside the frame that carries its key
// in data-fit-key is measured, so the caller also renders
// the cards in unmeasured, those not measured yet at this width, in a hidden
// container of no height beside the list: every page is then sized by its
// cards' real heights without the list ever growing past its space. Until a
// card is measured it counts as tall as the tallest one that was, and before
// any is, a page holds one card. The page shown is the one holding the card
// kept in view: show keeps a card, such as one just moved, and a turn keeps
// the first card of the page it turns to. frameRef is the box around the
// list, listRef the list itself.
export function useFit(keys: string[], frameRef: RefObject<HTMLDivElement | null>, listRef: RefObject<HTMLDivElement | null>) {
  const [layout, setLayout] = useState<Layout>({ width: -1, available: 0, gap: 0, columns: 1, heights: new Map() });
  const [kept, setKept] = useState({ key: "", page: 0 });
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const tallest = layout.heights.size ? Math.max(...layout.heights.values()) : Number.POSITIVE_INFINITY;
  const starts = listPageStarts(keys.map((key) => layout.heights.get(key) ?? tallest), layout.available, layout.gap, layout.columns);
  const page = pageShowing(keys.indexOf(kept.key), kept.page, starts);
  if (page !== kept.page) setKept({ key: kept.key, page });
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

  const show = (key: string) => setKept({ key, page });
  const turn = (step: number) => {
    const next = clampPage(page + step, starts.length);
    setKept({ key: keys[starts[next]] ?? "", page: next });
  };
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
  return { start, end, page, unmeasured, turn, show, onPointerDown, onPointerUp, onPointerCancel };
}
