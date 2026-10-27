import { useEffect, useId, useRef, useState } from "react";
import type { SessionStats, UsageReading } from "./session-usage";

export function ContextUsage({ stats, reading }: { stats: SessionStats; reading: UsageReading }) {
  const [isOpen, setOpen] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const id = useId();
  const percent = stats.contextPercent;
  const label = percent === undefined ? "Context usage unavailable" : `Context: ${Math.round(percent * 10) / 10}% used`;
  useEffect(() => {
    if (!isOpen) return;
    const close = (event: PointerEvent) => { if (event.target instanceof Node && !container.current?.contains(event.target)) setOpen(false); };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [isOpen]);
  return <div ref={container} className="context-usage"
    onPointerEnter={(event) => { if (event.pointerType === "mouse") setOpen(true); }}
    onPointerLeave={() => { if (document.activeElement !== button.current) setOpen(false); }}
    onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}
    onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); setOpen(false); } }}>
    <button ref={button} type="button" className="composer-control context-ring" aria-label={label} aria-expanded={isOpen} aria-describedby={isOpen ? id : undefined}
      onFocus={() => setOpen(true)} onClick={() => setOpen(true)}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><circle className="context-track" cx="12" cy="12" r="9" />
        {percent !== undefined && <circle className="context-used" cx="12" cy="12" r="9" pathLength="100" strokeDasharray={`${percent} 100`} />}
      </svg>
    </button>
    {isOpen && <div id={id} className="context-tooltip" role="tooltip" aria-label="Session statistics">
      <strong>{label}</strong>
      <dl className="usage-stats">
        {stats.details.map((stat) => <div key={stat.key}><dt>{stat.label}</dt><dd>{stat.text}</dd></div>)}
        {stats.missing.map((missing) => <div key={missing}><dt>Not read</dt><dd>{missing}</dd></div>)}
      </dl>
      {reading.banner ? <p>{reading.banner.text}: {reading.banner.detail}</p> : reading.why && <p>{reading.why}</p>}
    </div>}
  </div>;
}
