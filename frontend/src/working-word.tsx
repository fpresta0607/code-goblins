import { useEffect, useRef } from "react";

// The whimsical verbs a turn's words rotate through after the harness's own,
// goblin flavoured (the Overlord, 2026-10-29: "replaced with multiple
// different, not just working, but a bunch of synonyms, really cool,
// whimsical synonyms").
export const WORKING_WORDS = ["Conjuring", "Tinkering", "Scheming", "Rummaging", "Brewing", "Cobbling", "Scavenging", "Plotting", "Burrowing", "Smithing", "Muttering"];
// How long each letter takes to type, how long the shimmer takes to cross,
// and how long each word shows before the next.
const LETTER_MS = 55;
const SHIMMER_MS = 900;
export const WORD_MS = 3000;
const LONGEST_WORD = ["Working", ...WORKING_WORDS].reduce((wide, word) => word.length > wide.length ? word : wide);

// WorkingWord says what a turn does beside its helix: said, the harness's own
// words, or Working when it names nothing, then each of WORKING_WORDS in turn
// (the Overlord, 2026-10-30: "make twitsting and teh verbs rotate in a type
// writere hroiztonal shimmer"). Each word types itself out left to right, a
// letter at a time, then a shimmer crosses it. Both are transforms the
// compositor draws: the typing slides a window over the word to each letter's
// edge, measured once as the word comes. It writes each word into the page
// itself, so React draws nothing again while a turn runs. The room it takes
// is its longest word's, so nothing after it moves. Under reduced motion it
// shows said, or Working, whole and still.
export function WorkingWord({ said }: { said: string }) {
  const shown = useRef<HTMLSpanElement>(null);
  const letters = useRef<HTMLSpanElement>(null);
  const shine = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const [frame, text, light] = [shown.current, letters.current, shine.current];
    if (!frame || !text || !light) return;
    const words = [said || "Working", ...WORKING_WORDS.filter((word) => word !== said)];
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
      text.textContent = words[0];
      return;
    }
    let next = 0, timer = 0, running: Animation[] = [];
    const type = () => {
      running.forEach((animation) => animation.cancel());
      text.textContent = words[next];
      const edges = letterEdges(text);
      const width = edges.at(-1) ?? 0;
      const typed = edges.length * LETTER_MS;
      const typing: KeyframeAnimationOptions = { duration: typed, fill: "forwards" };
      const steps = (shift: (edge: number) => number) => [0, ...edges].map((edge) => ({ transform: `translateX(${shift(edge)}px)`, easing: "steps(1, end)" }));
      running = [
        frame.animate(steps((edge) => edge - width), typing),
        text.animate(steps((edge) => width - edge), typing),
        light.animate([{ transform: "translateX(-100%)" }, { transform: "translateX(100%)" }], { duration: SHIMMER_MS, delay: typed, easing: "ease-in-out", fill: "backwards" }),
      ];
      next = (next + 1) % words.length;
      timer = window.setTimeout(type, WORD_MS);
    };
    type();
    return () => {
      window.clearTimeout(timer);
      running.forEach((animation) => animation.cancel());
    };
  }, [said]);
  return <span className="reply-doing" data-room={said.length > LONGEST_WORD.length ? said : LONGEST_WORD} aria-hidden="true">
    <span className="working-word" ref={shown}><span ref={letters} /></span>
    <span className="working-shine" ref={shine} />
  </span>;
}

// letterEdges reads where each letter of element's one text ends, measured
// from its left edge.
function letterEdges(element: HTMLElement): number[] {
  const node = element.firstChild;
  if (!node) return [];
  const left = element.getBoundingClientRect().left;
  const range = document.createRange();
  let end = 0;
  return Array.from(node.textContent ?? "", (letter) => {
    end += letter.length;
    range.setStart(node, 0);
    range.setEnd(node, end);
    return range.getBoundingClientRect().right - left;
  });
}
