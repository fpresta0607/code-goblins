import type { ReactNode } from "react";
import { Disclosure } from "./Disclosure";
import "./afk.css";

// One heading of the AFK recap with its count and its lines, as one card.
// A folded heading, such as what was settled, opens at a tap.
export function AfkRecapGroup({ title, count, folded = false, children }: { title: string; count: number; folded?: boolean; children: ReactNode }) {
  const heading = <>{title} <span className="column-count">{count}</span></>;
  const rows = <ul className="afk-rows">{children}</ul>;
  return <section className="afk-group" aria-label={title}>
    {folded ? <Disclosure kind="afk-drawer" title={heading}>{rows}</Disclosure> : <><h4>{heading}</h4>{rows}</>}
  </section>;
}
