import type { MessagePicture } from "./session-messages";

// The pictures a prompt or a call's result holds, each read back from the
// supervisor by its id (picture), drawn in the message at no more than the
// list's width.
export function MessagePictures({ pictures, picture }: { pictures: MessagePicture[]; picture: (id: string) => string }) {
  if (!pictures.length) return null;
  return <div className="message-pictures">{pictures.map((each) => <img key={each.id} src={picture(each.id)} alt="Picture" loading="lazy" />)}</div>;
}
