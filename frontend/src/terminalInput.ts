export const maxInputBytes = 64 * 1024;
export interface TerminalCommand { type: "terminal.input" | "terminal.resize" | "terminal.scroll"; text?: string; cols?: number; rows?: number; direction?: "up" | "down"; lines?: number; source?: "wheel" | "page_key" }
export function queueInput(queue:TerminalCommand[],command:TerminalCommand) {
  const prior=queue[queue.length-1];
  const plain=(input:TerminalCommand)=>input.type==="terminal.input"&&!!input.text&&[...input.text].every(char=>char.charCodeAt(0)>=32&&char.charCodeAt(0)!==127);
  if(prior&&plain(prior)&&plain(command)&&inputBytes((prior.text||"")+(command.text||""))<=4096) prior.text=(prior.text||"")+(command.text||"");
  else queue.push(command);
}
export const inputBytes = (text: string) => new TextEncoder().encode(text).byteLength;
export function bracketedPaste(text: string): string {
  const input = "\x1b[200~" + text.replaceAll("\x1b[200~", "").replaceAll("\x1b[201~", "") + "\x1b[201~";
  if (inputBytes(input) > maxInputBytes) throw new Error("Paste exceeds 64 KiB. Paste a smaller selection; nothing was sent.");
  return input;
}
