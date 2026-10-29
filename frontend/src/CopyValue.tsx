import { useState } from "react";
import { copyText } from "./clipboard";
import { Icon } from "./Icon";
import { isWebLink, webLink, type CodeRenderer } from "./messageText";
import "./message-values.css";

const COPIED_MS = 1400;

// A value a goblin's message gives the Overlord to enter somewhere, such as a
// DNS record's name, with one button that copies exactly it. An address is
// only its link, opening in a new tab, with no copy beside it (the Overlord,
// 2026-10-28: "why is copy icon showing up").
export function CopyValue({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => copyText(value).then(() => { setCopied(true); setTimeout(() => setCopied(false), COPIED_MS); }, () => {});
  return <span className="copy-value">
    <code>{value}</code>
    <button type="button" className={"icon-button" + (copied ? " copied" : "")} aria-label={"Copy " + value} onClick={copy}><Icon name={copied ? "check" : "copy"} /></button>
  </span>;
}

export const copyValues: CodeRenderer = (value, key) => isWebLink(value) ? webLink(value, key) : <CopyValue key={key} value={value} />;
