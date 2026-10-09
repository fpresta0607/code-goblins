import { useEffect, useRef } from "react";

const BARS = 9;
// While listening, a bar takes the microphone's level this often.
const SAMPLE_MS = 70;
// A bar at rest is a dot, one eighth of its height, so while the microphone
// is held in silence the bars are a flat dotted line. A level under QUIET,
// such as a room's hum, keeps the dot: only a voice moves the bars.
const REST = 1 / 8;
const QUIET = 0.08;

// What a microphone shows while it listens: a red dot and a waveform that
// moves with the voice. Each bar is one recent sample of the microphone's
// level, newest on the right, and nothing runs once it stops listening.
export function VoiceBars({ level }: { level: () => number }) {
  const bars = useRef<(HTMLSpanElement | null)[]>([]);
  useEffect(() => {
    const levels = new Array<number>(BARS).fill(0);
    let frame = 0, sampled = 0;
    const draw = (now: number) => {
      if (now - sampled >= SAMPLE_MS) {
        sampled = now;
        levels.shift();
        levels.push(level());
        levels.forEach((value, index) => { const bar = bars.current[index]; if (bar) bar.style.transform = `scaleY(${value < QUIET ? REST : REST + (1 - REST) * value})`; });
      }
      frame = requestAnimationFrame(draw);
    };
    frame = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frame);
  }, [level]);
  return <><span className="voice-dot" aria-hidden="true" /><span className="voice-bars" aria-hidden="true">{Array.from({ length: BARS }, (_, index) => <span key={index} ref={(bar) => { bars.current[index] = bar; }} />)}</span></>;
}
