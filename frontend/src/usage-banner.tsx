import { useState } from "react";
import { Icon } from "./Icon";
import type { SessionStats } from "./session-usage";
import type { UsageReading } from "./session-usage";

// The session's usage above the composer: what it has spent and how much of
// its allowance that is, with one action that opens the full stats in place.
// It expands as a disclosure inside the panel, never a floating overlay, and
// its own box scrolls so the composer stays within reach. A session whose
// usage cannot be read draws no banner; the footer above says why.
export function UsageBanner({ reading, stats }: { reading: UsageReading; stats: SessionStats }) {
  const [isOpen, setOpen] = useState(false);
  const banner = reading.banner ?? (stats.hasUsage ? { text: "Session usage", detail: stats.details.filter((stat) => stat.key === "cost" || stat.key === "context").map((stat) => stat.text).join(" · ") } : null);
  if (!banner) return null;
  return <section className="usage-banner" aria-label="Session usage">
    <div className="usage-banner-line">
      {reading.banner && <Icon name="warning" />}
      <p className="usage-banner-words"><strong>{banner.text}</strong><span>{banner.detail}</span></p>
      <button type="button" className="labelled-button" aria-expanded={isOpen} onClick={() => setOpen((prior) => !prior)}>{isOpen ? "Hide usage" : "Usage"}</button>
    </div>
    {isOpen && <dl className="usage-stats">
      {stats.details.map((stat) => <div key={stat.key}><dt>{stat.label}</dt><dd>{stat.text}</dd></div>)}
      {stats.missing.map((missing) => <div key={missing}><dt>Not read</dt><dd>{missing}</dd></div>)}
    </dl>}
  </section>;
}
