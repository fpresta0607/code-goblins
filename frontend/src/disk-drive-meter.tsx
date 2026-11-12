import { memo } from "react";
import { diskMarks, diskScale, diskState, freeGigabytes } from "./start";

interface DiskDriveMeterProps { drive: string; isDevDrive: boolean; free: number; total: number; floor: number; wake: number; error: string }

// One drive the fleet writes to, named by its letter and, for the Dev Drive,
// as the Dev Drive, on a bar marked with the mark under which the CEO is woken
// and the floor under which no goblin or gate test run starts (see diskScale),
// whose tip says what each mark means. A drive that cannot be read says so on
// its line and draws no bar, so it never reads as room. Its props are its
// values alone, so a reading redraws only the drives it moved.
export const DiskDriveMeter = memo(function DiskDriveMeter({ drive, isDevDrive, free, total, floor, wake, error }: DiskDriveMeterProps) {
  const name = [drive, isDevDrive ? "Dev Drive" : ""].filter(Boolean).join(" ");
  // A narrow box breaks the line before the drive's name, never inside it.
  const label = name ? <>Disk free <span className="disk-drive-name">({name})</span></> : "Disk free";
  if (error) {
    const why = `${name || "The drive"} cannot be read: ${error}`;
    return <div className="disk-drive unreadable">
      <div className="memory-line" data-tip={why}><span>{label}</span><strong>Unreadable</strong></div>
      <p className="sr-only">{why}</p>
    </div>;
  }
  const reading = { free, total, floor, wake }, state = diskState(reading), scale = diskScale(reading);
  return <div className="disk-drive">
    <div className="memory-line"><span>{label}</span><strong>{freeGigabytes(free)} GB</strong></div>
    <div className="memory-bar" data-tip={diskMarks(reading)}>
      <span className={"memory-fill " + state.tone} style={{ width: `${scale.fill}%` }} />
      <span className="memory-mark floor" style={{ left: `${scale.wake}%` }} />
      <span className="memory-mark" style={{ left: `${scale.floor}%` }} />
    </div>
    <p className="sr-only">{state.text} {diskMarks(reading)}</p>
  </div>;
});
