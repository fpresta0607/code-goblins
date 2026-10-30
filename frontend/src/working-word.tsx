import { useEffect, useMemo, useRef } from "react";

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

// WorkingWord says what a turn does beside its helix: said, the harness's own
// words, or Working when it names nothing, then each of WORKING_WORDS in turn
// (the Overlord, 2026-10-30: "make twitsting and teh verbs rotate in a type
// writere hroiztonal shimmer"). Each word types itself out left to right, a
// letter at a time, then a shimmer crosses it. Both are transforms the
// compositor draws: the typing slides a window over the word to each letter's
// edge, measured once as the word comes, and the shimmer slides a soft band
// over a lit copy of the word, so it lights the letters and never the ground
// about them. It writes each word into the page itself, so React draws
// nothing again while a turn runs. The room it takes is its widest word's as
// drawn, every word laid unseen in the one place, so nothing after it moves,
// and a word wider than the row ends in an ellipsis. Under reduced motion it
// shows said, or Working, whole and still.
export function WorkingWord({ said }: { said: string }) {
  const shown = useRef<HTMLSpanElement>(null);
  const letters = useRef<HTMLSpanElement>(null);
  const shine = useRef<HTMLSpanElement>(null);
  const gleam = useRef<HTMLSpanElement>(null);
  const words = useMemo(() => [said || "Working", ...WORKING_WORDS.filter((word) => word !== said)], [said]);
  useEffect(() => {
    const [frame, text, light, lit] = [shown.current, letters.current, shine.current, gleam.current];
    if (!frame || !text || !light || !lit) return;
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
      text.textContent = words[0];
      return;
    }
    let next = 0, timer = 0, running: Animation[] = [];
    const type = () => {
      running.forEach((animation) => animation.cancel());
      text.textContent = words[next];
      lit.dataset.word = words[next];
      const edges = letterEdges(text);
      const width = edges.at(-1) ?? 0;
      const typed = edges.length * LETTER_MS;
      const typing: KeyframeAnimationOptions = { duration: typed, fill: "forwards" };
      const crossing: KeyframeAnimationOptions = { duration: SHIMMER_MS, delay: typed, easing: "ease-in-out", fill: "backwards" };
      const steps = (shift: (edge: number) => number) => [0, ...edges].map((edge) => ({ transform: `translateX(${shift(edge)}px)`, easing: "steps(1, end)" }));
      running = [
        frame.animate(steps((edge) => edge - width), typing),
        text.animate(steps((edge) => width - edge), typing),
        light.animate([{ transform: "translateX(-100%)" }, { transform: "translateX(100%)" }], crossing),
        lit.animate([{ transform: "translateX(100%)" }, { transform: "translateX(-100%)" }], crossing),
      ];
      next = (next + 1) % words.length;
      timer = window.setTimeout(type, WORD_MS);
    };
    type();
    return () => {
      window.clearTimeout(timer);
      running.forEach((animation) => animation.cancel());
    };
  }, [words]);
  return <span className="reply-doing" aria-hidden="true">
    {words.map((word) => <span className="working-room" data-word={word} key={word} />)}
    <span className="working-word" ref={shown}><span ref={letters} /></span>
    <span className="working-shine" ref={shine}><span ref={gleam} /></span>
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
