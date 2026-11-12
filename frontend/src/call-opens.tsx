import { useContext } from "react";
import { isBrowserCommand } from "./call-words";
import { Icon } from "./Icon";
import { RowOpener } from "./row-opener";

// The views a command's row opens, under the command and what it printed, as
// a terminal block in OpenBot does (#2362): the session's terminal, and its
// goblin's Screen view when the command drove that goblin's browser and the
// browser runs. A view the panel cannot show has no button.
export function CallOpens({ command }: { command: string }) {
  const { terminal, screen } = useContext(RowOpener);
  const opensScreen = !!screen && isBrowserCommand(command);
  if (!terminal && !opensScreen) return null;
  return <p className="call-opens">
    {opensScreen && <button type="button" className="text-button" onClick={screen}><Icon name="screen" />Open screen</button>}
    {terminal && <button type="button" className="text-button" onClick={terminal}><Icon name="terminal" />Open in terminal</button>}
  </p>;
}
