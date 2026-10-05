import type { Disk } from "./types";
import { diskScale, diskState, freeGigabytes } from "./start";

// Free disk on the home's drive, under the memory meter at the head of Tasks,
// on a bar marked with the mark under which the CFO is woken and the floor
// under which no goblin or gate test run starts (see diskScale); the fill's
// colour says which side of them the disk is on, and the same in words is for
// a screen reader. It shares the memory meter's look, labels included.
export function DiskMeter({ disk }: { disk: Disk }) {
  const state = diskState(disk), scale = diskScale(disk);
  return <div className="memory disk" role="group" aria-label="Disk">
    <div className="memory-line"><span>Disk free{disk.drive ? ` (${disk.drive})` : ""}</span><strong>{freeGigabytes(disk.free)} GB</strong></div>
    <div className="memory-bar" aria-hidden="true">
      <span className={"memory-fill " + state.tone} style={{ width: `${scale.fill}%` }} />
      <span className="memory-mark floor" style={{ left: `${scale.wake}%` }} />
      <span className="memory-mark" style={{ left: `${scale.floor}%` }} />
    </div>
    <div className="memory-scale" aria-hidden="true">
      <span className="floor" style={{ flexBasis: `${scale.wake}%` }}>{Math.round(disk.wake / 2 ** 30)} GB wake</span>
      <span className="gap" style={{ flexBasis: `${scale.floor - scale.wake}%` }} />
      <span className="next">{Math.round(disk.floor / 2 ** 30)} GB floor</span>
    </div>
    <p className="sr-only">{state.text}</p>
  </div>;
}
