import type { Memory } from "./types";
import { Icon } from "./Icon";
import { freeGigabytes, holdersLine, meterScale, meterState, poolWarning, tighter } from "./start";

// Free memory at the head of Tasks, on a bar marked with the floor under
// which nothing starts and the mark at which the CFO starts the next task
// (see meterScale); the fill's colour says which side of them memory is on,
// and the same in words is for a screen reader. Each mark's label sits at its
// mark while there is room, and gives way toward the left so that neither
// leaves the box when a mark is at the end of the bar. While commit (memory plus
// page file) is the tighter of the two, the meter shows commit instead and
// names the apps holding the most of it; a leaking paged pool gets a line of
// its own.
export function MemoryMeter({ memory }: { memory: Memory }) {
  const state = meterState(memory), scale = meterScale(memory), shown = tighter(memory);
  const holders = holdersLine(memory), warning = poolWarning(memory);
  return <div className="memory" role="group" aria-label="Memory">
    <div className="memory-line"><span>{shown.isCommit ? "Commit free (memory plus page file)" : "Memory free"}</span><strong>{freeGigabytes(shown.free)} GB</strong></div>
    <div className="memory-bar" aria-hidden="true">
      <span className={"memory-fill " + state.tone} style={{ width: `${scale.fill}%` }} />
      <span className="memory-mark floor" style={{ left: `${scale.floor}%` }} />
      <span className="memory-mark" style={{ left: `${scale.next}%` }} />
    </div>
    <div className="memory-scale" aria-hidden="true" style={{ gridTemplateColumns: `minmax(max-content, ${scale.floor}%) minmax(0, ${scale.next - scale.floor}%) max-content` }}>
      <span className="floor">{Math.round(memory.floor / 2 ** 30)} GB floor</span>
      <span className="next">{Math.round(memory.next / 2 ** 30)} GB next</span>
    </div>
    {holders && <p className="memory-holders">{holders}</p>}
    {warning && <p className="memory-warning"><Icon name="warning" />{warning}</p>}
    <p className="sr-only">{state.text}</p>
  </div>;
}
