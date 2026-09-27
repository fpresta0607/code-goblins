import type { Memory } from "./types";
import { freeGigabytes, meterState } from "./start";

const percent = (part: number, whole: number) => `${Math.min(100, Math.max(0, part / whole * 100))}%`;

// Free memory at the head of Tasks, against the mark at which the CFO starts
// the next task; the bar spans the machine's memory.
export function MemoryMeter({ memory }: { memory: Memory }) {
  const state = meterState(memory);
  return <div className="memory" role="group" aria-label="Memory">
    <div className="memory-line"><span>Memory free</span><strong>{freeGigabytes(memory.available)} GB</strong></div>
    <div className="memory-bar" aria-hidden="true">
      <span className={"memory-fill " + state.tone} style={{ width: percent(memory.available, memory.total) }} />
      <span className="memory-mark" style={{ left: percent(memory.next, memory.total) }}><span>{Math.round(memory.next / 2 ** 30)} GB</span></span>
    </div>
    <p className="memory-state">{state.text}</p>
  </div>;
}
