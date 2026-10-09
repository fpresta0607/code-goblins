import type { Disk, Memory, Scheduling } from "./types";
import { DiskMeter } from "./DiskMeter";
import { freeGigabytes, holdersLine, memoryMarks, meterScale, meterState, poolWarning, scheduleLine, tighter } from "./start";

// Free memory at the head of Tasks: its name and value, a bar marked with the
// floor under which nothing starts and the mark at which the next task starts
// (see meterScale), whose tip says what each mark means, and with memory free
// a line naming what the supervisor started or resumed, or why nothing
// waiting started; the fill's colour says which side of them memory is on,
// and the same in words is for a screen reader. While commit (memory plus
// page file) is the tighter of the two, the meter shows commit instead, and
// its bar's tip names the apps holding the most of it; a leaking paged pool
// is named there too. Free disk, when the snapshot has it, is the second
// meter in the same box, under memory. Nothing under a bar is more words than
// that, as the Overlord asked on 2026-10-07 ("dont need extra text under").
export function MemoryMeter({ memory, scheduling = null, disk = null }: { memory: Memory; scheduling?: Scheduling | null; disk?: Disk | null }) {
  const state = meterState(memory, scheduling), scale = meterScale(memory), shown = tighter(memory), scheduled = scheduleLine(memory, scheduling);
  const tip = [memoryMarks(memory), holdersLine(memory), poolWarning(memory)].filter(Boolean).join(" ");
  return <div className="memory" role="group" aria-label="Memory">
    <div className="memory-line"><span>{shown.isCommit ? "Commit free (memory plus page file)" : "Memory free"}</span><strong>{freeGigabytes(shown.free)} GB</strong></div>
    <div className="memory-bar" data-tip={tip}>
      <span className={"memory-fill " + state.tone} style={{ width: `${scale.fill}%` }} />
      <span className="memory-mark floor" style={{ left: `${scale.floor}%` }} />
      <span className="memory-mark" style={{ left: `${scale.next}%` }} />
    </div>
    {scheduled && <p className="memory-schedule" aria-hidden="true">{scheduled}</p>}
    <p className="sr-only">{state.text} {tip}</p>
    {disk && <DiskMeter disk={disk} />}
  </div>;
}
