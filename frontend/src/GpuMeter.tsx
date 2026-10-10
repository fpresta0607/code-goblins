import type { Gpu } from "./types";
import { freeShare, gpuFree, gpuMarks } from "./start";

// How free the busiest graphics adapter is, the last meter in the memory
// meter's box, on a bar with no mark, since no start waits on the GPU; its
// tip names each adapter, how busy it is and the app using most of it, and
// the same in words is for a screen reader. It shares the memory meter's
// look, with nothing written under its bar.
export function GpuMeter({ gpu }: { gpu: Gpu }) {
  const free = gpuFree(gpu), tip = gpuMarks(gpu);
  return <div className="disk-meter" role="group" aria-label="GPU">
    <div className="memory-line"><span>GPU free</span><strong>{freeShare(free)}</strong></div>
    <div className="memory-bar" data-tip={tip} data-tip-align="start">
      <span className="memory-fill ready" style={{ width: freeShare(free) }} />
    </div>
    <p className="sr-only">{tip}</p>
  </div>;
}
