import type { Processors } from "./types";
import { freeShare, processorMarks, processorScale, processorState } from "./start";

// How free the performance cores are, a meter in the memory meter's box under
// disk, on a bar marked where the supervisor starts the next task by itself
// (see processorScale), whose tip names the cores by kind, how busy each kind
// is and the apps using them most; the fill's colour says which side of the
// mark the cores are on, and the same in words is for a screen reader. It
// shares the memory meter's look, with nothing written under its bar.
export function ProcessorMeter({ processors }: { processors: Processors }) {
  const state = processorState(processors), scale = processorScale(processors), tip = processorMarks(processors);
  return <div className="disk-meter" role="group" aria-label="CPU">
    <div className="memory-line"><span>CPU free</span><strong>{freeShare(processors.free)}</strong></div>
    <div className="memory-bar" data-tip={tip} data-tip-align="start">
      <span className={"memory-fill " + state.tone} style={{ width: `${scale.fill}%` }} />
      <span className="memory-mark" style={{ left: `${scale.next}%` }} />
    </div>
    <p className="sr-only">{state.text} {tip}</p>
  </div>;
}
