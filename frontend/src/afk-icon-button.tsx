import type { ButtonHTMLAttributes } from "react";
import { Icon, type IconName } from "./Icon";
import "./afk.css";

// A button of the AFK recap dressed as the Command Center's: a raised round
// button that is its mark alone, the words being its name for a screen reader
// and its tip for a pointer.
export function AfkIconButton({ icon, label, ...rest }: { icon: IconName; label: string } & Omit<ButtonHTMLAttributes<HTMLButtonElement>, "aria-label" | "children">) {
  return <button {...rest} className={"icon-button raised afk-icon-button " + (rest.className || "")} aria-label={label} data-tip={label}><Icon name={icon} /></button>;
}
