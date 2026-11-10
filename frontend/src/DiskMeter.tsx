import type { Disk } from "./types";
import { DiskDriveMeter } from "./disk-drive-meter";

// Free disk on each drive the fleet writes to, the home's, the Dev Drive's and
// the gate home's, one line and bar for each (see DiskDriveMeter), the second
// meter in the memory meter's box. Two folders on one drive are one line, and
// every start and wake still reads the least free of the home's drive and the
// Dev Drive. It shares the memory meter's look.
export function DiskMeter({ disk }: { disk: Disk }) {
  return <div className="disk-meter" role="group" aria-label="Disk">
    {disk.drives.map((drive) => <DiskDriveMeter key={drive.drive} drive={drive.drive} isDevDrive={drive.dev_drive} free={drive.free} total={drive.total} floor={disk.floor} wake={disk.wake} error={drive.error} />)}
  </div>;
}
