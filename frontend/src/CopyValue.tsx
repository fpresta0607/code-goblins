import { useState } from "react";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";
import { isWebLink, webLink, type CodeRenderer } from "./messageText";
import "./message-values.css";

const COPIED_MS = 1400;

// A value a goblin's message gives the Overlord to enter somewhere, such as a
// DNS record's name, with one button that copies exactly it. An address is a
// link too, opening in a new tab.
export function CopyValue({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => copyText(value).then(() => { setCopied(true); setTimeout(() => setCopied(false), COPIED_MS); }, () => {});
  return <span className="copy-value">
    {isWebLink(value) ? webLink(value, 0) : <code>{value}</code>}
    <button type="button" className="icon-button" aria-label={"Copy " + value} data-tip={copied ? "Copied" : "Copy"} onClick={copy}><Icon name={copied ? "check" : "copy"} /></button>
  </span>;
}

export const copyValues: CodeRenderer = (value, key) => <CopyValue key={key} value={value} />;
