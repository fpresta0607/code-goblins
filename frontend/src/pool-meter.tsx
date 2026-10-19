import type { Memory } from "./types";
import { kernelPools, poolMarks, poolScale, poolState } from "./start";

// The kernel's paged and nonpaged pools together, the meter under memory in
// the memory meter's box, on a bar marked at the line past which free memory
// can no longer hold the fleet (see poolScale), whose tip gives each pool and
// the tags that grew most since the sign-in began; the fill turns red past the
// line, where the CFO's panel offers Restart PC, and the same in words is for
// a screen reader. It shares the memory meter's look, with nothing written
// under its bar.
export function PoolMeter({ memory }: { memory: Memory }) {
  const state = poolState(memory), scale = poolScale(memory), tip = poolMarks(memory);
  return <div className="disk-meter" role="group" aria-label="Kernel pools">
    <div className="memory-line"><span>Kernel pools</span><strong>{(kernelPools(memory) / 2 ** 30).toFixed(1)} GB</strong></div>
    <div className="memory-bar" data-tip={tip}>
      <span className={"memory-fill " + state.tone} style={{ width: `${scale.fill}%` }} />
      <span className="memory-mark floor" style={{ left: `${scale.line}%` }} />
    </div>
    <p className="sr-only">{state.text} {tip}</p>
  </div>;
}
