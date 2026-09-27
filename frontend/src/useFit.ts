import { useLayoutEffect, useRef, useState, type PointerEvent, type RefObject } from "react";
import { availableHeight, clampPage, pageSizeFor, swipeStep, tallestCard } from "./fit";

const columnsOf = (grid: HTMLElement) => {
  const value = getComputedStyle(grid).gridTemplateColumns;
  return value === "none" ? 1 : value.split(" ").length;
};

// Fits a list of count cards to the board's visible canvas: the page holds as
// many rows of the tallest card seen as fit below the list (see
// availableHeight), times the columns its grid lays out, and a sideways swipe
// turns it. frameRef is the box around the list, listRef the list itself.
export function useFit(count: number, frameRef: RefObject<HTMLDivElement | null>, listRef: RefObject<HTMLDivElement | null>) {
  const [size, setSize] = useState(Math.max(1, count));
  const [chosen, setChosen] = useState(0);
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const page = clampPage(chosen, size, count);

  useLayoutEffect(() => {
    const frame = frameRef.current, list = listRef.current;
    if (!frame || !list) return;
    const canvas = frame.closest<HTMLElement>(".canvas-region") ?? document.documentElement;
    const board = frame.closest<HTMLElement>(".task-board");
    const column = frame.closest<HTMLElement>(".board-column") ?? frame;
    let tallest = { width: -1, unit: 0 };
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
      tallest = tallestCard(tallest, list.clientWidth, [...list.children].map((card) => (card as HTMLElement).offsetHeight));
      const next = pageSizeFor(available, tallest.unit, parseFloat(getComputedStyle(list).rowGap) || 0, columnsOf(list));
      setSize((prior) => prior === next ? prior : next);
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
  }, [count, frameRef, listRef]);

  const turn = (step: number) => setChosen(clampPage(page + step, size, count));
  const onPointerDown = (event: PointerEvent<HTMLElement>) => {
    swipe.current = event.pointerType === "touch" ? { x: event.clientX, y: event.clientY } : null;
  };
  const onPointerUp = (event: PointerEvent<HTMLElement>) => {
    const start = swipe.current;
    swipe.current = null;
    if (start) turn(swipeStep(event.clientX - start.x, event.clientY - start.y));
  };
  return { size, page, start: page * size, turn, onPointerDown, onPointerUp };
}
