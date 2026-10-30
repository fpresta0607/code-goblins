import { Fragment, useEffect, useMemo, useRef } from "react";

// The whimsical verbs a turn's words rotate through after the harness's own,
// goblin flavoured (the Overlord, 2026-10-29: "replaced with multiple
// different, not just working, but a bunch of synonyms, really cool,
// whimsical synonyms").
const WORKING_WORDS = ["Conjuring", "Tinkering", "Scheming", "Rummaging", "Brewing", "Cobbling", "Scavenging", "Plotting", "Burrowing", "Smithing", "Muttering"];
// How long each letter takes to type, how long the shimmer takes to cross,
// and how long each word shows before the next.
const LETTER_MS = 55;
const SHIMMER_MS = 900;
const WORD_MS = 3000;
const HELD = "steps(1, end)";

// WorkingWord says what a turn does beside its helix: said, the harness's own
// words, or Working when it names nothing, then each of WORKING_WORDS in turn
// (the Overlord, 2026-10-30: "make twitsting and teh verbs rotate in a type
// writere hroiztonal shimmer"). Each word types itself out left to right, a
// letter at a time, then a shimmer crosses it. Every word is in the page from
// the start, laid unseen in the one place, and shows in its turn by its own
// animations, which come round again for as long as the turn runs, so the
// page itself never changes while the words rotate and the compositor draws
// it all. The typing slides a window over the word to each letter's edge,
// measured as the words come and again when their room changes size, as when
// a font loads or the panel narrows; the shimmer slides a soft band over a
// lit copy of the word, so it lights the letters and never the ground about
// them. The room the words take is their widest's as drawn, so nothing after
// them moves, and a word wider than the row ends in an ellipsis. Under
// reduced motion it shows said, or Working, whole and still.
export function WorkingWord({ said }: { said: string }) {
  const doing = useRef<HTMLSpanElement>(null);
  const words = useMemo(() => [said || "Working", ...WORKING_WORDS.filter((word) => word !== said)], [said]);
  useEffect(() => {
    const room = doing.current;
    if (!room || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const [frames, texts, lights, lits] = [".working-word", ".working-word > span", ".working-shine", ".working-shine > span"].map((selector) => [...room.querySelectorAll<HTMLElement>(":scope > " + selector)]);
    const cycle = words.length * WORD_MS;
    const turning: KeyframeAnimationOptions = { id: "working-word", duration: cycle, iterations: Infinity };
    let started: CSSNumberish | null = null, running: Animation[] = [];
    const play = () => {
      running.forEach((animation) => animation.cancel());
      running = words.flatMap((_, at) => {
        const start = at * WORD_MS, end = start + WORD_MS;
        const edges = letterEdges(texts[at]);
        const width = edges.at(-1) ?? 0;
        const typed = start + edges.length * LETTER_MS;
        // Each typing step holds until the next, and the word goes when its
        // turn ends; a word too long to type and cross in its turn has its
        // shimmer cross faster.
        const steps = (shift: (edge: number) => number): Keyframe[] => [
          { offset: 0, transform: `translateX(${shift(0)}px)`, easing: HELD },
          ...edges.map((edge, letter) => ({ offset: (start + (letter + 1) * LETTER_MS) / cycle, transform: `translateX(${shift(edge)}px)`, easing: HELD })),
          { offset: end / cycle, transform: `translateX(${shift(0)}px)`, easing: HELD },
          { offset: 1, transform: `translateX(${shift(0)}px)` },
        ];
        const crossing = (side: number): Keyframe[] => [
          { offset: 0, transform: `translateX(${-side * 100}%)`, easing: HELD },
          { offset: typed / cycle, transform: `translateX(${-side * 100}%)`, easing: "ease-in-out" },
          { offset: Math.min(typed + SHIMMER_MS, end) / cycle, transform: `translateX(${side * 100}%)`, easing: HELD },
          { offset: 1, transform: `translateX(${side * 100}%)` },
        ];
        return [
          frames[at].animate(steps((edge) => edge - width), turning),
          texts[at].animate(steps((edge) => width - edge), turning),
          lights[at].animate(crossing(1), turning),
          lits[at].animate(crossing(-1), turning),
        ];
      });
      // Measured again, the words keep their place in the rotation.
      started ??= document.timeline.currentTime;
      running.forEach((animation) => { animation.startTime = started; });
    };
    const resized = new ResizeObserver(play);
    resized.observe(room);
    return () => {
      resized.disconnect();
      running.forEach((animation) => animation.cancel());
    };
  }, [words]);
  return <span className="reply-doing" aria-hidden="true" ref={doing}>
    {words.map((word) => <Fragment key={word}>
      <span className="working-word"><span>{word}</span></span>
      <span className="working-shine"><span data-word={word} /></span>
    </Fragment>)}
  </span>;
}

// letterEdges reads where each letter of element's one text ends, measured
// from its left edge; a letter its ellipsis hides ends at its right edge.
function letterEdges(element: HTMLElement): number[] {
  const node = element.firstChild;
  if (!node) return [];
  const { left, right } = element.getBoundingClientRect();
  const range = document.createRange();
  let end = 0;
  return Array.from(node.textContent ?? "", (letter) => {
    end += letter.length;
    range.setStart(node, 0);
    range.setEnd(node, end);
    return Math.min(range.getBoundingClientRect().right, right) - left;
  });
}
