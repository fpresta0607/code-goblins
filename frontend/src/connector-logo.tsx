import { connectorLogo } from "./connectors";
import "./connector-logo.css";

// Every logo the board carries, by its file, each written by the build under
// a name of its content, so a browser keeps it (#1966), and never into the
// script, so it is fetched only where it is seen.
const LOGOS = import.meta.glob<string>("./connector-logos/*.svg", { query: "?no-inline", import: "default", eager: true });

// A connector's own logo on a tile: a file bundled with the board, fetched
// only once a logo is about to be seen, or the connector's initial when the
// board carries no logo for it, so no connector ever shows a broken picture
// or another service's mark. isInline draws it at the size of a line's icon,
// without the tile, as a call's row in a conversation does.
export function ConnectorLogo({ name, isInline = false }: { name: string; isInline?: boolean }) {
  const logo = connectorLogo(name);
  return <span className={"connector-logo" + (isInline ? " is-inline" : "")} aria-hidden="true">
    {"file" in logo
      ? <img src={LOGOS["./connector-logos/" + logo.file + ".svg"]} alt="" width="24" height="24" loading="lazy" decoding="async" />
      : <span className="connector-initial">{logo.initial}</span>}
  </span>;
}
