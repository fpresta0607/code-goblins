import { useContext } from "react";
import { isBrowserCommand } from "./call-words";
import { Icon } from "./Icon";
import { RowOpener } from "./row-opener";

// The view a command's row opens, under the command and what it printed
// (#2362): its goblin's Screen view when the command drove that goblin's
// browser and the browser runs. A row has no button for the session's
// terminal (#2524: "we dont need the open in terminal button"), which the
// panel's style menu shows.
export function CallOpens({ command }: { command: string }) {
  const { screen } = useContext(RowOpener);
  if (!screen || !isBrowserCommand(command)) return null;
  return <p className="call-opens">
    <button type="button" className="text-button" onClick={screen}><Icon name="screen" />Open screen</button>
  </p>;
}
