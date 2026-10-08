import type { Disk } from "./types";
import { diskMarks, diskScale, diskState, freeGigabytes } from "./start";

// Free disk on the home's drive, the second meter in the memory meter's box,
// on a bar marked with the mark under which the CFO is woken and the floor
// under which no goblin or gate test run starts (see diskScale), whose tip
// says what each mark means; the fill's colour says which side of them the
// disk is on, and the same in words is for a screen reader. It shares the
// memory meter's look.
export function DiskMeter({ disk }: { disk: Disk }) {
  const state = diskState(disk), scale = diskScale(disk);
  return <div className="disk-meter" role="group" aria-label="Disk">
    <div className="memory-line"><span>Disk free{disk.drive ? ` (${disk.drive})` : ""}</span><strong>{freeGigabytes(disk.free)} GB</strong></div>
    <div className="memory-bar" data-tip={diskMarks(disk)} data-tip-align="start">
      <span className={"memory-fill " + state.tone} style={{ width: `${scale.fill}%` }} />
      <span className="memory-mark floor" style={{ left: `${scale.wake}%` }} />
      <span className="memory-mark" style={{ left: `${scale.floor}%` }} />
    </div>
    <p className="sr-only">{state.text} {diskMarks(disk)}</p>
  </div>;
}
