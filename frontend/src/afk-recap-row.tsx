import { useId, useState, type ReactNode } from "react";
import { Chevron } from "./Chevron";
import { Icon } from "./Icon";
import { afkTime, linkLabel, safeLink } from "./afk";
import "./afk.css";

// Tone is the colour of a row's mark and its second line: done, answered,
// or a warning, for what did not land or needs him.
export type Tone = "done" | "answered" | "warning";

// One line of the AFK recap: its mark, what was done or decided in plain
// words, with its link, a second line that says how it ended or what was
// answered, and its time. What it stood on is one tap away, under Evidence,
// never inline. action is a button of the row's own, such as Change, and
// children what the row lists under its words, such as its pull requests.
export function AfkRecapRow({ mark, what, link = "", detail = "", tone = "done", at = "", now, evidence = "", struck = false, action, children }: {
  mark: ReactNode; what: string; link?: string; detail?: string; tone?: Tone; at?: string; now: number; evidence?: string; struck?: boolean; action?: ReactNode; children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const shown = useId();
  const href = safeLink(link);
  // A link that says what the row says, such as a pull request merged, is
  // the row's words themselves.
  const named = !!href && linkLabel(href) === what;
  const said = named ? <a href={href} target="_blank" rel="noreferrer">{what}</a> : what;
  const words = struck ? <s>{said}</s> : said;
  return <li className={"afk-row " + tone}>
    <span className="afk-row-mark">{mark}</span>
    <div className="afk-row-text">
      <span className="afk-row-what">{words}</span>
      {children}
      {detail && <small className="afk-row-detail">{detail}</small>}
      {open && <p id={shown} className="afk-row-evidence">{evidence}</p>}
    </div>
    <div className="afk-row-side">
      {href && !named && <a className="afk-row-link" href={href} target="_blank" rel="noreferrer"><Icon name="external" />{linkLabel(href)}</a>}
      {action}
      {evidence && <button className="text-button afk-evidence-toggle" aria-expanded={open} aria-controls={open ? shown : undefined} onClick={() => setOpen(!open)}>Evidence<Chevron collapsed={!open} /></button>}
      {at && <time dateTime={at}>{afkTime(at, now)}</time>}
    </div>
  </li>;
}
