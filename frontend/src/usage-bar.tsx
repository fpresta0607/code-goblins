import type { UsageReading } from "./session-usage";
import "./usage-bar.css";

// A reading this full or fuller draws its slice in a warning colour, and one
// at FULL_PERCENT or more in the danger colour.
const HIGH_PERCENT = 85;
const FULL_PERCENT = 95;

// The session's usage as small pies, one for each reading the session backs,
// each in a colour of its own with its percentage beside it, in the message
// box's own dress (the Overlord, 2026-10-28: "Claude uses a bunch of
// different colors to show smaller little circle pie graphs, pie slices,
// percentages, relevant to the context of the actual conversation"). With no
// reading it draws nothing at all: never an empty pie, never "unavailable".
// Each pie is a meter, so a screen reader reads its name, its share and the
// numbers behind it, and nothing opens on hover.
export function UsageBar({ readings }: { readings: UsageReading[] }) {
  if (!readings.length) return null;
  return <div className="usage-bar" role="group" aria-label="Usage">
    {readings.map((reading) => {
      const shown = reading.percent > 0 && reading.percent < 1 ? "<1%" : Math.round(reading.percent) + "%";
      const level = reading.percent >= FULL_PERCENT ? "full" : reading.percent >= HIGH_PERCENT ? "high" : undefined;
      return <span key={reading.key} className={"usage-reading usage-" + reading.key} data-level={level} role="meter" aria-label={reading.label}
        aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(reading.percent * 10) / 10} aria-valuetext={shown + " used, " + reading.detail}>
        <svg className="usage-pie" viewBox="0 0 20 20" aria-hidden="true">
          <circle className="usage-pie-track" cx="10" cy="10" r="9" />
          <circle className="usage-pie-slice" cx="10" cy="10" r="4.5" pathLength="100" strokeDasharray={reading.percent + " 100"} />
        </svg>
        <span className="usage-label" aria-hidden="true">{reading.label}</span>
        <span className="usage-percent" aria-hidden="true">{shown}</span>
      </span>;
    })}
  </div>;
}
