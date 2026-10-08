import type { DevDriveView } from "./types.ts";

// devDriveOffer is what the first run says of a Dev Drive, once, until it is
// answered: an opt-in where this machine can have one or has one unused, one
// plain line where it cannot, and nothing once the Overlord answered, where
// the board has no such setting, or where the home is on one already.
export function devDriveOffer(drive: DevDriveView | undefined): "offer" | "unavailable" | null {
  if (!drive || drive.choice !== "") return null;
  if (drive.state === "absent" || drive.state === "present") return "offer";
  return drive.state === "unavailable" ? "unavailable" : null;
}
