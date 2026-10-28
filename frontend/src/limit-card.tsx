import { Icon } from "./Icon";
import { useTerminalLink } from "./terminal-link";
import { tryAgain } from "./try-again";
import "./limit.css";

// Where Claude Code's own usage settings are, which the card opens in the
// browser.
const USAGE_SETTINGS = "https://claude.ai/settings/usage";
// What Claude Code's screen says while it waits to continue on its own.
const CONTINUES = /continuing automatically/i;

// LimitCard is a usage limit's notice in the conversation, as Claude's own
// app draws one (the Overlord, 2026-10-28): "Session limit reached", when it
// resets, whether the session continues on its own then, Try again and Open
// usage settings, and never the harness's own words for it or its purchase
// offer. terminal is the key of the session's terminal while this is the
// limit the session is at now, and empty for one it is past, which shows only
// what it was. Unchecking Auto-continue cancels Claude Code's own wait
// (Escape), which it cannot take back, so the box stays unchecked after;
// Try again asks the session to continue (try-again.ts).
export function LimitCard({ resets, terminal }: { resets: string; terminal: string }) {
  const link = useTerminalLink(terminal);
  const panel = link?.screen?.panel;
  const continues = !!panel && CONTINUES.test([...panel.rows, ...panel.hints].join(" "));
  return <section className="limit-card" aria-label="Session limit reached">
    <p className="limit-card-head"><Icon name="gauge" />Session limit reached</p>
    <div className="limit-card-body">
      {resets && <p className="limit-card-when">{continues ? "Auto-resuming at " : "Resets at "}{resets}</p>}
      {terminal && <>
        <label className="limit-card-continue">
          <input type="checkbox" checked={continues} disabled={!continues || !link} onChange={() => link?.escape()} />
          Auto-continue when limits reset
        </label>
        <div className="limit-card-actions">
          <button type="button" disabled={!link} onClick={() => { if (link) tryAgain(terminal, link); }}>Try again</button>
          <a className="labelled-button primary" href={USAGE_SETTINGS} target="_blank" rel="noreferrer"><Icon name="gauge" />Open usage settings</a>
        </div>
      </>}
    </div>
  </section>;
}
