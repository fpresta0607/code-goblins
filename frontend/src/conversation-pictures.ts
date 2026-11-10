import { callWords } from "./call-words.ts";
import { keptPictures } from "./kept-files.ts";
import type { MessageBlock, SessionMessage } from "./session-messages.ts";

// The pictures of a conversation as its rows show them, each by the address
// it is read back from: one the transcript holds, by its id (picture), and
// one the message box kept, by its path (keptPicture). Both addresses are
// the board's own routes, which answer only to its token, and no row names a
// file by where it is on the disk.

// A picture a call read, took or produced, with the verb its call is told
// by, which for a connector's call is its name and its tool in words.
export interface CallPicture { src: string; verb: string }

// callPictures is the pictures a row of calls shows at its end, in the order
// the calls ran: one call's own, or those of every call folded into the row.
export function callPictures(calls: MessageBlock[], picture: (id: string) => string): CallPicture[] {
  return calls.flatMap((call) => {
    const words = callWords(call.tool, false);
    const verb = words.action ? words.verb + " · " + words.action : words.verb;
    return call.pictures.map((each) => ({ src: picture(each.id), verb }));
  });
}

// promptPictures is the pictures he sent with a prompt: those the transcript
// holds, then those the box kept and his words name by path. A command's
// words are what he typed, never a kept picture.
export function promptPictures(message: SessionMessage, picture: (id: string) => string, keptPicture: (path: string) => string): string[] {
  const isCommand = message.source === "slash" || message.source === "shell";
  return [...message.pictures.map((each) => picture(each.id)), ...(isCommand ? [] : keptPictures(message.text).paths.map(keptPicture))];
}

// conversationPictures is every picture the conversation's rows show, once
// each, in the order they show them: the set its viewer steps through. A
// notice draws no picture, so it adds none.
export function conversationPictures(messages: SessionMessage[], picture: (id: string) => string, keptPicture: (path: string) => string): string[] {
  return [...new Set(messages.flatMap((message) => {
    if (message.kind === "reply") return callPictures(message.blocks, picture).map((each) => each.src);
    return message.kind === "prompt" && message.source !== "notice" ? promptPictures(message, picture, keptPicture) : [];
  }))];
}
